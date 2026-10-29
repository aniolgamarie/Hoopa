//go:build linux && amd64

package launch

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"hoopa/internal/config"
)

var applicationRunMutex sync.Mutex

func duplicateApplicationStdio(files ...*os.File) (out []*os.File, err error) {
	defer func() {
		if err != nil {
			for _, f := range out {
				_ = f.Close()
			}
			out = nil
		}
	}()
	for i, f := range files {
		if f == nil {
			return out, ErrLaunchUnavailable
		}
		fd, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), syscall.F_DUPFD_CLOEXEC, 3)
		if errno != 0 {
			return out, ErrLaunchUnavailable
		}
		copy := os.NewFile(fd, "hoopa-app-stdio")
		if copy == nil {
			_ = syscall.Close(int(fd))
			return out, ErrLaunchUnavailable
		}
		out = append(out, copy)
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
		if errno != 0 || i == 0 && int(flags)&syscall.O_ACCMODE == syscall.O_WRONLY || i > 0 && int(flags)&syscall.O_ACCMODE == syscall.O_RDONLY {
			return out, ErrLaunchUnavailable
		}
	}
	return out, nil
}

// Run reserves durable identity before forking a fixed gated helper. The caller
// has captured the profile and independently qualified any managed endpoint.
// After authorization it waits for the original child despite context expiry;
// only explicit wrapper-directed signals are forwarded to the application.
func Run(ctx context.Context, options RunOptions) (result RunResult, err error) {
	result.Code = 4
	if ctx == nil || options.App.ID != options.Profile.App.ID || options.App.Metering {
		result.Code = 2
		return result, ErrLaunchArguments
	}
	env, e := options.Environment.SensitiveValues()
	if e != nil {
		result.Code = 2
		return result, ErrLaunchArguments
	}
	payload, e := encodeLaunchPayload(launchPayload{Argv: options.Argv, Env: env})
	if e != nil {
		result.Code = 2
		return result, ErrLaunchArguments
	}
	if !applicationRunMutex.TryLock() {
		return result, ErrLaunchUnavailable
	}
	defer applicationRunMutex.Unlock()
	stdio, e := duplicateApplicationStdio(options.Stdin, options.Stdout, options.Stderr)
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	defer func() {
		for _, f := range stdio {
			_ = f.Close()
		}
	}()
	terminal, e := prepareTerminal(stdio[0])
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	wrapper, e := currentIdentity()
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	setup, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	writer, e := config.ReserveAppLaunch(setup, options.Paths, config.AppLaunchInput{Profile: options.Profile, Mode: options.App.Mode, Metering: options.App.Metering, EntryBindingSHA256: options.EntryBindingSHA256, Wrapper: wrapper})
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	defer func() {
		if writer.Close() != nil {
			result.Code = 5
			err = ErrLaunchBookkeeping
		}
	}()
	reservation, e := writer.TakeSpawn()
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	result.LaunchID = reservation.LaunchID
	var pipes []*os.File
	defer func() {
		for _, f := range pipes {
			_ = f.Close()
		}
	}()
	newPipe := func() (*os.File, *os.File, error) {
		r, w, e := os.Pipe()
		if e == nil {
			pipes = append(pipes, r, w)
		}
		return r, w, e
	}
	pr, pw, e := newPipe()
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	gr, gw, e := newPipe()
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	sr, sw, e := newPipe()
	if e != nil {
		return result, ErrLaunchUnavailable
	}
	if setup.Err() != nil {
		return result, ErrLaunchUnavailable
	}
	pidfd := -1
	cmd := &exec.Cmd{Path: "/proc/self/exe", Args: []string{"hoopa", AppLauncherEntry}, Env: []string{}, Stdin: stdio[0], Stdout: stdio[1], Stderr: stdio[2], ExtraFiles: []*os.File{pr, gr, sw}, SysProcAttr: terminal.attributes(&pidfd)}
	// Foreground transfer happens only after original pidfd + birth are known.
	// The fixed helper never touches user stdin before the subsequent GO.
	cmd.SysProcAttr.Foreground = false
	cmd.SysProcAttr.Ctty = 0
	signals := make(chan os.Signal, 16)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	defer signal.Stop(signals)
	if e = cmd.Start(); e != nil {
		if pidfd >= 0 {
			_ = syscall.Close(pidfd)
		}
		return result, ErrLaunchUnavailable
	}
	_ = pr.Close()
	_ = gr.Close()
	_ = sw.Close()
	// Never repair a missing clone pidfd by opening a later numeric PID. Some
	// emulated kernels may start without returning one; no foreground transfer or
	// authorization has occurred, and EOF makes this helper exit without exec.
	if pidfd < 0 {
		_ = pw.Close()
		_ = gw.Close()
		_ = sr.Close()
		_ = cmd.Wait()
		return result, ErrLaunchUnavailable
	}
	stopSignals := make(chan struct{})
	signalsDone := make(chan struct{})
	var signalFailed atomic.Bool
	go func() {
		defer close(signalsDone)
		for {
			select {
			case <-stopSignals:
				return
			case s := <-signals:
				if e := signalOriginalChild(pidfd, s.(syscall.Signal)); e != nil && !errors.Is(e, syscall.ESRCH) {
					signalFailed.Store(true)
				}
			}
		}
	}()
	var stopOnce sync.Once
	stopForwarding := func() { stopOnce.Do(func() { signal.Stop(signals); close(stopSignals); <-signalsDone }) }
	defer func() { stopForwarding(); _ = syscall.Close(pidfd) }()

	payloadDone := make(chan error, 1)
	go func() {
		n, e := pw.Write(payload)
		if e == nil && n != len(payload) {
			e = io.ErrShortWrite
		}
		closeErr := pw.Close()
		if e == nil {
			e = closeErr
		}
		payloadDone <- e
	}()
	readyDone := make(chan bool, 1)
	go func() {
		var status [1]byte
		_, e := io.ReadFull(sr, status[:])
		readyDone <- e == nil && status[0] == 'R'
	}()
	ready, payloadWritten := false, false
	preparationFailed := false
	for !ready || !payloadWritten {
		select {
		case e := <-payloadDone:
			payloadDone = nil
			if e != nil {
				preparationFailed = true
			} else {
				payloadWritten = true
			}
		case ok := <-readyDone:
			readyDone = nil
			if !ok {
				preparationFailed = true
			} else {
				ready = true
			}
		case <-setup.Done():
			preparationFailed = true
		}
		if preparationFailed {
			break
		}
	}
	var child config.AppLaunchProcess
	birthRecorded, authorized, foregroundAttempted := false, false, false
	wrapperFailure := ""
	if !preparationFailed {
		child, e = childIdentity(cmd.Process.Pid)
		if e == nil {
			e = writer.RecordBirth(setup, child)
		}
		if e == nil {
			birthRecorded = true
			foregroundAttempted, e = terminal.handoff(child)
		}
		if e == nil {
			e = writer.Authorize(setup)
		}
		if e == nil {
			authorized = true
			if n, sendErr := gw.Write([]byte{'G'}); sendErr != nil || n != 1 {
				wrapperFailure = "launcher_protocol_failed"
			}
		} else {
			preparationFailed = true
		}
	}
	// Closing a failed/expired preauthorization gate cannot release user code.
	// Once authorization is recorded, a send failure is conservatively unknown.
	_ = gw.Close()
	_ = pw.Close()
	if !authorized {
		wrapperFailure = "dispatch_aborted"
		if e := signalOriginalChild(pidfd, syscall.SIGKILL); e != nil && !errors.Is(e, syscall.ESRCH) {
			wrapperFailure = "signal_forward_failed"
		}
	}
	// Closing status is only necessary on failed setup, to join a pending reader.
	if preparationFailed {
		_ = sr.Close()
	}
	if payloadDone != nil {
		<-payloadDone
	}
	if readyDone != nil {
		<-readyDone
	}
	// No Wait goroutine: keep original child unreaped for terminal restoration.
	if e = observeChildExit(context.Background(), pidfd); e != nil {
		wrapperFailure = "launcher_protocol_failed"
	}
	if child.PID == 0 {
		child, _ = childIdentity(cmd.Process.Pid)
	}
	if foregroundAttempted {
		if e = terminal.restore(child); e != nil {
			wrapperFailure = "tty_restore_failed"
		}
	}
	waitErr := cmd.Wait()
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) || cmd.ProcessState == nil {
		result.Code = 5
		return result, ErrLaunchBookkeeping
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Exited() && !status.Signaled() {
		result.Code = 5
		return result, ErrLaunchBookkeeping
	}
	observed := config.AppLaunchResult{WaitStatus: uint32(status)}
	if status.Exited() {
		code := status.ExitStatus()
		observed.ExitCode = &code
		result.Code = code
	} else {
		sig := int(status.Signal())
		observed.Signal = &sig
		result.Code = 128 + sig
	}
	if authorized && ready && !preparationFailed {
		raw, readErr := io.ReadAll(io.LimitReader(sr, 4))
		if readErr != nil {
			wrapperFailure = "launcher_protocol_failed"
		} else if len(raw) != 0 {
			if len(raw) == 2 && raw[0] == 'E' {
				reason, expected := applicationExecReason(raw[1])
				if reason != "" && observed.ExitCode != nil && *observed.ExitCode == expected {
					observed.ExecFailureReason = &reason
				} else {
					wrapperFailure = "launcher_protocol_failed"
				}
			} else {
				wrapperFailure = "launcher_protocol_failed"
			}
		}
	}
	// Finish forwarding before final failure projection and pidfd close.
	stopForwarding()
	if signalFailed.Load() {
		wrapperFailure = "signal_forward_failed"
	}
	if wrapperFailure != "" {
		observed.WrapperFailure = &wrapperFailure
	}
	result.Observed = &observed
	if !birthRecorded {
		result.Code = 5
		return result, ErrLaunchBookkeeping
	}
	bookkeeping, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	if e = writer.RecordTerminal(bookkeeping, observed); e != nil || wrapperFailure != "" {
		result.Code = 5
		return result, ErrLaunchBookkeeping
	}
	return result, nil
}
