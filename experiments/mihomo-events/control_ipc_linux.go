//go:build linux && amd64

package mihomoevents

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var ErrControlIPC = errors.New("invalid or unavailable private control IPC")

// CrashAt is an explicit isolated-guest scenario option. The private seams are
// only injectable by package tests; ordinary service has no crash hook.
type ControlServeOptions struct {
	CrashAt string
	Ready   func()
	peerUID func(net.Conn) (uint32, error)
	exit    func(int)
	guard   func() error
}

func controlInstancePath(directory string) error {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsRune(directory, 0) || len(filepath.Join(directory, "control.sock")) > 107 {
		return ErrControlIPC
	}
	return nil
}
func InitControlInstance(directory string) error {
	if e := controlInstancePath(directory); e != nil {
		return e
	}
	if e := os.Mkdir(directory, 0700); e != nil {
		return ErrControlIPC
	}
	if e := controlCheckPath(directory, true); e != nil {
		return ErrControlIPC
	}
	var random [32]byte
	if _, e := rand.Read(random[:]); e != nil {
		return ErrControlIPC
	}
	token := append([]byte(hex.EncodeToString(random[:])), '\n')
	if e := linuxControlWriteExclusive(filepath.Join(directory, "control.token"), token); e != nil {
		return ErrControlIPC
	}
	if e := InitControlProbe(filepath.Join(directory, "store")); e != nil {
		return ErrControlIPC
	}
	if e := controlSyncDirectory(directory); e != nil {
		return ErrControlIPC
	}
	if e := controlSyncDirectory(filepath.Dir(directory)); e != nil {
		return ErrControlIPC
	}
	return nil
}
func controlSocketInfo(path string) (os.FileInfo, error) {
	v, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	st, ok := v.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Geteuid() || v.Mode()&os.ModeType != os.ModeSocket || v.Mode().Perm() != 0600 || v.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, ErrControlIPC
	}
	return v, nil
}
func controlInstanceMaterial(directory string) (string, error) {
	if controlInstancePath(directory) != nil || controlCheckPath(directory, true) != nil {
		return "", ErrControlIPC
	}
	entries, e := os.ReadDir(directory)
	if e != nil {
		return "", ErrControlIPC
	}
	seen := map[string]bool{}
	for _, v := range entries {
		switch v.Name() {
		case "store":
			if controlCheckPath(filepath.Join(directory, v.Name()), true) != nil {
				return "", ErrControlIPC
			}
		case "control.token":
			if controlCheckPath(filepath.Join(directory, v.Name()), false) != nil {
				return "", ErrControlIPC
			}
		case "control.sock":
			if _, e = controlSocketInfo(filepath.Join(directory, v.Name())); e != nil {
				return "", ErrControlIPC
			}
		default:
			return "", ErrControlIPC
		}
		seen[v.Name()] = true
	}
	if !seen["store"] || !seen["control.token"] {
		return "", ErrControlIPC
	}
	raw, e := linuxControlReadFile(filepath.Join(directory, "control.token"), 65)
	if e != nil || len(raw) != 65 || raw[64] != '\n' || !controlHex.Match(raw[:64]) {
		return "", ErrControlIPC
	}
	return string(raw[:64]), nil
}

type controlPeerKey struct{}
type controlPeer struct {
	UID   uint32
	Valid bool
}

func controlPeerUID(c net.Conn) (uint32, error) {
	u, ok := c.(*net.UnixConn)
	if !ok {
		return 0, ErrControlIPC
	}
	raw, e := u.SyscallConn()
	if e != nil {
		return 0, ErrControlIPC
	}
	var cred *syscall.Ucred
	var inner error
	e = raw.Control(func(fd uintptr) {
		cred, inner = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if e != nil || inner != nil || cred == nil {
		return 0, ErrControlIPC
	}
	return cred.Uid, nil
}

// ServeControlInstance blocks until cancellation or a listener failure. Closing
// the service releases control ownership; it never calls backend.Stop.
func ServeControlInstance(ctx context.Context, directory string, backend ControlBackend, options ControlServeOptions) (result error) {
	if controlInstancePath(directory) != nil {
		return ErrControlIPC
	}
	if actual, ok := backend.(*LinuxControlBackend); ok {
		relative, e := filepath.Rel(directory, actual.assetsFile)
		if e != nil || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
			return ErrControlIPC
		}
	}
	if options.CrashAt != "" {
		if options.CrashAt != "spawn-returned-before-record" {
			return ErrControlIPC
		}
		guard := options.guard
		if guard == nil {
			guard = linuxControlGuestGuard
		}
		if e := guard(); e != nil {
			return e
		}
	}
	token, e := controlInstanceMaterial(directory)
	if e != nil {
		return e
	}
	p, e := OpenControlProbe(filepath.Join(directory, "store"), backend)
	if e != nil {
		return e
	}
	defer func() {
		if p.Close() != nil {
			result = ErrControlIPC
		}
	}()
	if options.CrashAt != "" {
		exit := options.exit
		if exit == nil {
			exit = os.Exit
		}
		p.hook = func(point string) error {
			if point == options.CrashAt {
				exit(86)
				return ErrControlPoisoned
			}
			return nil
		}
	}
	socket := filepath.Join(directory, "control.sock")
	if _, e = os.Lstat(socket); e == nil {
		if _, e = controlSocketInfo(socket); e != nil {
			return ErrControlIPC
		}
		if e = os.Remove(socket); e != nil {
			return ErrControlIPC
		}
		if e = controlSyncDirectory(directory); e != nil {
			return ErrControlIPC
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return ErrControlIPC
	}
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if e != nil {
		return ErrControlIPC
	}
	listener.SetUnlinkOnClose(false)
	var owned os.FileInfo
	defer func() {
		_ = listener.Close()
		current, e := os.Lstat(socket)
		if errors.Is(e, os.ErrNotExist) {
			return
		}
		if e != nil || owned == nil || !os.SameFile(current, owned) {
			result = ErrControlIPC
			return
		}
		if _, e = controlSocketInfo(socket); e != nil {
			result = ErrControlIPC
			return
		}
		if os.Remove(socket) != nil || controlSyncDirectory(directory) != nil {
			result = ErrControlIPC
		}
	}()
	if os.Chmod(socket, 0600) != nil {
		return ErrControlIPC
	}
	owned, e = controlSocketInfo(socket)
	if e != nil || controlSyncDirectory(directory) != nil {
		return ErrControlIPC
	}
	peerUID := options.peerUID
	if peerUID == nil {
		peerUID = controlPeerUID
	}
	managerUID := uint32(os.Geteuid())
	server := &http.Server{ErrorLog: log.New(io.Discard, "", 0), Handler: controlIPCHandler(p, token, managerUID), MaxHeaderBytes: 4096, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 35 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 2 * time.Second, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		uid, e := peerUID(c)
		return context.WithValue(ctx, controlPeerKey{}, controlPeer{UID: uid, Valid: e == nil})
	}}
	// Serve can fail independently of context cancellation. Close accepted
	// connections on every exit path, before releasing the controller lock.
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	if options.Ready != nil {
		options.Ready()
	}
	select {
	case e = <-done:
		if !errors.Is(e, http.ErrServerClosed) {
			return ErrControlIPC
		}
		return nil
	case <-ctx.Done():
		server.SetKeepAlivesEnabled(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		e = server.Shutdown(shutdownCtx)
		cancel()
		if e != nil {
			_ = server.Close()
		}
		<-done
		return nil
	}
}

type controlOperationProjection struct {
	ID             string `json:"operation_id"`
	Action         string `json:"action"`
	Phase          string `json:"phase"`
	Step           string `json:"step"`
	BaseRevision   uint64 `json:"base_revision"`
	TargetRevision uint64 `json:"target_revision"`
}

func controlProjectOperation(o ControlOperation) *controlOperationProjection {
	if o.ID == "" {
		return nil
	}
	return &controlOperationProjection{ID: o.ID, Action: o.Request.Action, Phase: o.Phase, Step: o.Step, BaseRevision: o.Base.Revision, TargetRevision: o.TargetRevision}
}

type controlIPCReply struct {
	Version   int                         `json:"version"`
	Operation *controlOperationProjection `json:"operation,omitempty"`
	ErrorCode string                      `json:"error_code,omitempty"`
}
type controlIPCStatus struct {
	Version        int                          `json:"version"`
	ActiveRevision uint64                       `json:"active_revision"`
	Desired        string                       `json:"desired"`
	Observation    string                       `json:"observation"`
	Instance       *ControlInstance             `json:"instance,omitempty"`
	Poisoned       bool                         `json:"poisoned"`
	Pending        []controlOperationProjection `json:"pending"`
	ErrorCode      string                       `json:"error_code,omitempty"`
}

func controlIPCError(err error) (int, string) {
	switch {
	case err == nil:
		return 200, ""
	case errors.Is(err, ErrControlConflict):
		return 409, "CONFLICT"
	case errors.Is(err, ErrControlIDConflict):
		return 409, "ID_CONFLICT"
	case errors.Is(err, ErrControlPending):
		return 409, "PENDING"
	case errors.Is(err, ErrControlUnknown), errors.Is(err, ErrLinuxControlRuntime):
		return 503, "UNKNOWN"
	case errors.Is(err, ErrControlPoisoned):
		return 503, "POISONED"
	case errors.Is(err, ErrControlClosed):
		return 503, "CLOSED"
	default:
		return 500, "ACTION_FAILED"
	}
}
func controlIPCWrite(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func controlIPCReject(w http.ResponseWriter, status int, code string) {
	controlIPCWrite(w, status, controlIPCReply{Version: 1, ErrorCode: code})
}
func controlIPCBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if e != nil || len(raw) == 0 {
		return nil, ErrControlIPC
	}
	return raw, nil
}
func controlIPCHandler(p *ControlProbe, token string, uid uint32) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		peer, _ := r.Context().Value(controlPeerKey{}).(controlPeer)
		auth := r.Header.Values("Authorization")
		if !peer.Valid || peer.UID != uid || len(auth) != 1 || len(auth[0]) != len("Bearer ")+64 || !strings.HasPrefix(auth[0], "Bearer ") || subtle.ConstantTimeCompare([]byte(auth[0][len("Bearer "):]), []byte(token)) != 1 {
			controlIPCReject(w, 401, "UNAUTHORIZED")
			return
		}
		if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.RequestURI != r.URL.Path {
			controlIPCReject(w, 404, "INVALID_ARGUMENT")
			return
		}
		if r.Header.Get("Upgrade") != "" || strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			controlIPCReject(w, 422, "INVALID_ARGUMENT")
			return
		}
		switch r.URL.Path {
		case "/v1/status":
			if r.Method != "GET" {
				controlIPCReject(w, 405, "INVALID_ARGUMENT")
				return
			}
			if r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			raw, e := io.ReadAll(io.LimitReader(r.Body, 1))
			r.Body.Close()
			if e != nil || len(raw) != 0 {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			s, err := p.Status(r.Context())
			status, code := controlIPCError(err)
			reply := controlIPCStatus{Version: 1, ActiveRevision: s.Active.Revision, Desired: s.Active.Desired, Observation: s.Observation.State, Poisoned: s.Poisoned, Pending: []controlOperationProjection{}, ErrorCode: code}
			if s.Observation.State == ControlVerifiedRunning {
				reply.Instance = controlClone(s.Observation.Instance)
			}
			for _, o := range s.Pending {
				reply.Pending = append(reply.Pending, *controlProjectOperation(o))
			}
			controlIPCWrite(w, status, reply)
		case "/v1/execute":
			if r.Method != "POST" {
				controlIPCReject(w, 405, "INVALID_ARGUMENT")
				return
			}
			raw, e := controlIPCBody(w, r)
			if e != nil {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			request, e := DecodeControlRequest(raw)
			if e != nil {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			o, e := p.Execute(r.Context(), request)
			status, code := controlIPCError(e)
			controlIPCWrite(w, status, controlIPCReply{Version: 1, Operation: controlProjectOperation(o), ErrorCode: code})
		case "/v1/recover":
			if r.Method != "POST" {
				controlIPCReject(w, 405, "INVALID_ARGUMENT")
				return
			}
			raw, e := controlIPCBody(w, r)
			if e != nil {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			var request struct {
				Version          int    `json:"version"`
				OperationID      string `json:"operation_id"`
				ExpectedRevision uint64 `json:"expected_revision"`
			}
			if controlDecode(raw, &request) != nil || request.Version != 1 || !controlToken.MatchString(request.OperationID) {
				controlIPCReject(w, 422, "INVALID_ARGUMENT")
				return
			}
			o, e := p.Recover(r.Context(), request.OperationID, request.ExpectedRevision)
			status, code := controlIPCError(e)
			controlIPCWrite(w, status, controlIPCReply{Version: 1, Operation: controlProjectOperation(o), ErrorCode: code})
		default:
			controlIPCReject(w, 404, "INVALID_ARGUMENT")
		}
	})
}
