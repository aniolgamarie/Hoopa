//go:build linux && amd64

package launch

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// RunAppLauncher is the fixed local self-exec entry, dispatched before flags,
// Store access or any service initialization. It never accepts a saved launch
// ID, caller-chosen executable argument, RPC, or filesystem payload path.
func RunAppLauncher(args []string) int {
	if len(args) != 1 || args[0] != AppLauncherEntry {
		return 125
	}
	files := make([]*os.File, 3)
	var inodes [3]uint64
	for i := range files {
		fd := i + 3
		var st syscall.Stat_t
		flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
		want := syscall.O_RDONLY
		if i == 2 {
			want = syscall.O_WRONLY
		}
		if errno != 0 || int(flags)&syscall.O_ACCMODE != want || syscall.Fstat(fd, &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFIFO {
			return 125
		}
		if _, _, errno = syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, syscall.FD_CLOEXEC); errno != 0 {
			return 125
		}
		inodes[i] = st.Ino
		files[i] = os.NewFile(uintptr(fd), "hoopa-app-control")
		if files[i] == nil {
			return 125
		}
		defer files[i].Close()
	}
	if inodes[0] == inodes[1] || inodes[0] == inodes[2] || inodes[1] == inodes[2] {
		return 125
	}
	fail := func() int { _, _ = files[2].Write([]byte{'P'}); return 125 }
	payload, e := decodeLaunchPayload(files[0])
	if e != nil {
		return fail()
	}
	if files[0].Close() != nil {
		return fail()
	}
	if n, e := files[2].Write([]byte{'R'}); e != nil || n != 1 {
		return 125
	}
	var gate [1]byte
	if _, e := io.ReadFull(files[1], gate[:]); e != nil || gate[0] != 'G' {
		return fail()
	}
	if n, e := io.ReadFull(files[1], gate[:]); n != 0 || e != io.EOF {
		return fail()
	}
	if files[1].Close() != nil {
		return fail()
	}
	// Success does not return. Status remains CLOEXEC, so neither the application
	// nor a background descendant can keep the protocol pipe open after exec.
	err := execApplication(payload)
	code, reason := classifyApplicationExec(err)
	_, _ = files[2].Write([]byte{'E', reason})
	return code
}

func execApplication(payload launchPayload) error {
	argv := payload.Argv
	if strings.Contains(argv[0], "/") {
		return syscall.Exec(argv[0], argv, payload.Env)
	}
	var path string
	found := false
	for _, item := range payload.Env {
		if strings.HasPrefix(item, "PATH=") {
			path = strings.TrimPrefix(item, "PATH=")
			found = true
			break
		}
	}
	if !found {
		return syscall.ENOENT
	}
	permission := false
	for _, dir := range strings.Split(path, ":") {
		err := syscall.Exec(filepath.Join(dir, argv[0]), argv, payload.Env)
		switch {
		case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR):
		case errors.Is(err, syscall.EACCES):
			permission = true
		default:
			return err
		}
	}
	if permission {
		return syscall.EACCES
	}
	return syscall.ENOENT
}
func classifyApplicationExec(err error) (int, byte) {
	switch {
	case errors.Is(err, syscall.ENOEXEC):
		return 126, 1
	case errors.Is(err, syscall.ENOENT), errors.Is(err, syscall.ENOTDIR):
		return 127, 2
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return 126, 3
	default:
		return 126, 4
	}
}
func applicationExecReason(code byte) (string, int) {
	switch code {
	case 1:
		return "exec_format", 126
	case 2:
		return "exec_not_found", 127
	case 3:
		return "exec_permission", 126
	case 4:
		return "exec_unavailable", 126
	}
	return "", 0
}
