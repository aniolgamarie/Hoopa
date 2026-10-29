//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/control"
	"hoopa/internal/mihomo"
	"hoopa/internal/service"
)

const readinessLimit = 4096
const readinessTimeout = 10 * time.Second

var errBootstrap = errors.New("management bootstrap unavailable")
var errWriterBusy = errors.New("management writer busy")

type readiness struct {
	Version  int             `json:"version"`
	Nonce    string          `json:"nonce"`
	Instance string          `json:"instance,omitempty"`
	Revision config.Revision `json:"revision,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func lowerHex(value string, size int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == size && hex.EncodeToString(decoded) == value
}
func runCoreLauncher(ctx context.Context, args []string, diagnostics io.Writer) int {
	if len(args) != 1 || os.Getenv("HOOPA_LAUNCH_PROTOCOL") != "1" || !lowerHex(os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), 32) || !lowerHex(os.Getenv("HOOPA_RUN_NONCE"), 32) {
		safeDiagnostic(diagnostics)
		return 2
	}
	directory := os.NewFile(3, "core-birth")
	// Stat neither reads configured paths nor opens a second Store writer.
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		directory.Close()
		safeDiagnostic(diagnostics)
		return 2
	}
	if err = mihomo.ExecBasicLauncher(ctx, directory, os.Getenv("HOOPA_LAUNCH_INTENT_SHA256"), os.Getenv("HOOPA_RUN_NONCE")); err != nil {
		safeDiagnostic(diagnostics)
		return 5
	}
	return 5
}
func safeDiagnostic(w io.Writer) {
	if w != nil {
		_, _ = io.WriteString(w, "hoopa: internal operation unavailable\n")
	}
}
func runServiceChild(ctx context.Context, args []string, diagnostics io.Writer) int {
	nonce := os.Getenv("HOOPA_SERVICE_NONCE")
	if len(args) != 1 || os.Getenv("HOOPA_SERVICE_PROTOCOL") != "1" || !lowerHex(nonce, 24) {
		safeDiagnostic(diagnostics)
		return 2
	}
	var stat syscall.Stat_t
	if err := syscall.Fstat(3, &stat); err != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFIFO {
		safeDiagnostic(diagnostics)
		return 2
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, 3, syscall.F_GETFL, 0)
	if errno != 0 || flags&syscall.O_ACCMODE != syscall.O_WRONLY {
		safeDiagnostic(diagnostics)
		return 2
	}
	// Register the inherited pipe as pollable before wrapping it. Calling Fd on
	// the wrapped file later would undo this and break write cancellation.
	if err := syscall.SetNonblock(3, true); err != nil {
		safeDiagnostic(diagnostics)
		return 2
	}
	ready := os.NewFile(3, "management-readiness")
	defer ready.Close()
	var err error
	sent := false
	send := func(value readiness) error {
		raw, err := json.Marshal(value)
		if err != nil || len(raw) > readinessLimit {
			return errBootstrap
		}
		if err = ready.SetWriteDeadline(time.Now().Add(readinessTimeout)); err != nil {
			return errBootstrap
		}
		n, err := ready.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		err = errors.Join(err, ready.Close())
		return err
	}
	paths, err := resolvePaths()
	if err == nil {
		err = service.Run(ctx, paths, service.Options{LauncherArgs: []string{coreLauncherEntry}, Ready: func(info service.ReadyInfo) error {
			if sent {
				return errBootstrap
			}
			sent = true
			return send(readiness{Version: 1, Nonce: nonce, Instance: info.Instance, Revision: info.Revision})
		}})
	}
	if !sent {
		reason := "unavailable"
		if errors.Is(err, config.ErrWriterBusy) {
			reason = "writer-busy"
		}
		_ = send(readiness{Version: 1, Nonce: nonce, Error: reason})
	}
	if err != nil {
		safeDiagnostic(diagnostics)
		return 4
	}
	return 0
}
func serviceEnvironment(nonce string) []string {
	env := []string{}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "TMPDIR", "LANG"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			env = append(env, key+"="+value)
		}
	}
	if os.Getenv("LANG") == "" {
		env = append(env, "LANG=C")
	}
	return append(env, "HOOPA_SERVICE_PROTOCOL=1", "HOOPA_SERVICE_NONCE="+nonce)
}
func privateLog(root *os.Root, nonce string) (*os.File, error) {
	file, err := root.OpenFile(".service-log-"+nonce+".log", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errBootstrap
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, errBootstrap
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		file.Close()
		return nil, errBootstrap
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return nil, errBootstrap
	}
	directory, err := root.Open(".")
	if err != nil {
		file.Close()
		return nil, errBootstrap
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		file.Close()
		return nil, errBootstrap
	}
	return file, nil
}

// readReadiness never calls File.Fd: doing so turns the pipe blocking and can
// disable its Go read deadline. The cancellation callback is fenced on return.
func readReadiness(ctx context.Context, reader *os.File, nonce string) (result readiness, err error) {
	if ctx == nil || reader == nil {
		return result, errBootstrap
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	deadline := time.Now().Add(readinessTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err = reader.SetReadDeadline(deadline); err != nil {
		return result, errBootstrap
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = reader.Close(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
		if cause := ctx.Err(); cause != nil {
			err = cause
		}
	}()
	raw, err := io.ReadAll(io.LimitReader(reader, readinessLimit+1))
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return result, context.DeadlineExceeded
		}
		return result, errBootstrap
	}
	if len(raw) > readinessLimit || len(raw) == 0 {
		return result, errBootstrap
	}
	canonical, err := config.CanonicalJSON(raw)
	if err != nil {
		return result, errBootstrap
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || result.Version != 1 || result.Nonce != nonce {
		return readiness{}, errBootstrap
	}
	if result.Error != "" {
		if result.Instance != "" || result.Revision != "" {
			return readiness{}, errBootstrap
		}
		if result.Error == "writer-busy" {
			return result, errWriterBusy
		}
		return result, errBootstrap
	}
	if !lowerHex(result.Instance, 24) {
		return readiness{}, errBootstrap
	}
	if _, err := config.ParseID(string(result.Revision)); err != nil {
		return readiness{}, errBootstrap
	}
	return result, nil
}
func existingService(ctx context.Context, paths config.Paths) (config.Revision, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	for {
		conn, err := service.DialEndpoint(bounded, paths)
		if err == nil {
			if err = conn.Close(); err != nil {
				return "", errBootstrap
			}
			status, err := service.LocalStatus(bounded, paths)
			return status.Revision, err
		}
		if !errors.Is(err, service.ErrServiceUnavailable) {
			return "", err
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return "", bounded.Err()
		case <-timer.C:
		}
	}
}

// bootstrap performs at most one detached spawn. Only this newly spawned direct
// child may be killed/reaped on pre-readiness failure; a ready service outlives
// the frontend and no core is started by this path.
func bootstrap(ctx context.Context, paths config.Paths) (config.Revision, error) {
	if ctx == nil {
		return "", control.ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	conn, err := service.DialEndpoint(ctx, paths)
	if err == nil {
		if err = conn.Close(); err != nil {
			return "", errBootstrap
		}
		status, err := service.LocalStatus(ctx, paths)
		return status.Revision, err
	}
	if !errors.Is(err, service.ErrServiceUnavailable) {
		return "", err
	}
	store, err := config.OpenStore(paths, true)
	if err != nil {
		if errors.Is(err, config.ErrWriterBusy) {
			return existingService(ctx, paths)
		}
		return "", err
	}
	if err = store.Close(); err != nil {
		return "", err
	}
	root, err := config.OpenRuntimeDirectory(paths)
	if err != nil {
		return "", err
	}
	defer root.Close()
	nonce, err := randomHex(24)
	if err != nil {
		return "", err
	}
	log, err := privateLog(root, nonce)
	if err != nil {
		return "", err
	}
	defer log.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		return "", errBootstrap
	}
	defer reader.Close()
	defer writer.Close()
	if err = ctx.Err(); err != nil {
		return "", err
	}
	executable, err := os.Executable()
	if err != nil {
		return "", errBootstrap
	}
	cmd := exec.Command("/proc/self/exe")
	cmd.Args = []string{executable, serviceEntry}
	cmd.Env = serviceEnvironment(nonce)
	cmd.Dir = paths.Runtime
	cmd.ExtraFiles = []*os.File{writer}
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = cmd.Start(); err != nil {
		return "", errBootstrap
	}
	writer.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	info, err := readReadiness(ctx, reader, nonce)
	if err != nil {
		_ = cmd.Process.Kill()
		<-exited
		if errors.Is(err, errWriterBusy) {
			return existingService(ctx, paths)
		}
		return "", err
	}
	return info.Revision, nil
}
