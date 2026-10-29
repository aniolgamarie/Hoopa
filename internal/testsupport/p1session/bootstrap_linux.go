//go:build linux && amd64 && p1integration

// Package p1session bridges an authenticated test supervisor FD to a manager
// whose real parent is a remote session shell. It is not a product API or a
// security boundary against hostile same-UID processes.
package p1session

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/testsupport/p1trace"
)

const MaxPacket = 1024

var ErrBootstrap = errors.New("P1_SESSION_BOOTSTRAP_FAILED")

type Request struct {
	Version          int    `json:"version"`
	Stream           string `json:"stream"`
	ManagerPID       int    `json:"manager_pid"`
	ManagerParentPID int    `json:"manager_parent_pid"`
}
type Response struct {
	Version int    `json:"version"`
	Stream  string `json:"stream"`
}

func validStream(stream string) bool {
	raw, err := hex.DecodeString(stream)
	return err == nil && len(raw) == 16 && hex.EncodeToString(raw) == stream
}
func strict(raw []byte, out any, fields ...string) bool {
	if len(raw) == 0 || len(raw) > MaxPacket {
		return false
	}
	if _, err := config.CanonicalJSON(raw); err != nil {
		return false
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != len(fields) {
		return false
	}
	for _, field := range fields {
		if object[field] == nil {
			return false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out) == nil
}
func privateParent(path string, uid int) (*os.File, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 107 || strings.ContainsRune(path, 0) {
		return nil, ErrBootstrap
	}
	directory, err := os.Open("/")
	if err != nil {
		return nil, ErrBootstrap
	}
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), "/"), "/") {
		if part == "" {
			continue
		}
		fd, err := syscall.Openat(int(directory.Fd()), part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		directory.Close()
		if err != nil {
			return nil, ErrBootstrap
		}
		directory = os.NewFile(uintptr(fd), "private-broker-parent")
	}
	info, err := directory.Stat()
	if err != nil {
		directory.Close()
		return nil, ErrBootstrap
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Mode&07777 != 0700 || st.Uid != uint32(uid) {
		directory.Close()
		return nil, ErrBootstrap
	}
	socket, err := os.Lstat(path)
	if err != nil {
		directory.Close()
		return nil, ErrBootstrap
	}
	ss, ok := socket.Sys().(*syscall.Stat_t)
	if !ok || socket.Mode()&os.ModeSocket == 0 || socket.Mode()&os.ModeSymlink != 0 || ss.Uid != uint32(uid) {
		directory.Close()
		return nil, ErrBootstrap
	}
	return directory, nil
}

// readPacket closes every received rights FD even on rejected/truncated input.
// Go's UnixConn receive path requests atomic MSG_CMSG_CLOEXEC on Linux; we also
// verify the flag before exposing the sole accepted descriptor to NewProducer.
func readPacket(conn *net.UnixConn, pid, uid int) (data []byte, rights []int, eof bool, err error) {
	raw, oob := make([]byte, MaxPacket+1), make([]byte, syscall.CmsgSpace(16*4)+syscall.CmsgSpace(12))
	n, on, flags, _, readErr := conn.ReadMsgUnix(raw, oob)
	control, parseErr := syscall.ParseSocketControlMessage(oob[:on])
	valid := parseErr == nil
	var credential *syscall.Ucred
	for _, message := range control {
		if message.Header.Level == syscall.SOL_SOCKET && message.Header.Type == syscall.SCM_RIGHTS {
			fds, e := syscall.ParseUnixRights(&message)
			rights = append(rights, fds...)
			if e != nil {
				valid = false
			}
		} else if message.Header.Level == syscall.SOL_SOCKET && message.Header.Type == syscall.SCM_CREDENTIALS && credential == nil {
			var e error
			credential, e = syscall.ParseUnixCredentials(&message)
			if e != nil {
				valid = false
			}
		} else {
			valid = false
		}
	}
	defer func() {
		if err != nil {
			for _, fd := range rights {
				syscall.Close(fd)
			}
			rights = nil
		}
	}()
	if !valid || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 || n > MaxPacket {
		return nil, rights, false, ErrBootstrap
	}
	if readErr == io.EOF && n == 0 && on == 0 {
		return nil, rights, true, nil
	}
	if readErr != nil {
		return nil, rights, false, ErrBootstrap
	}
	// With SO_PASSCRED every actual packet, including an empty one, carries
	// credentials. A zero read without ancillary data is therefore EOF.
	if n == 0 && on == 0 {
		return nil, rights, true, nil
	}
	if len(control) != 2 || len(rights) != 1 || credential == nil || int(credential.Pid) != pid || int(credential.Uid) != uid {
		return nil, rights, false, ErrBootstrap
	}
	return raw[:n], rights, false, nil
}

// Connect requires one strict response plus exactly one SCM_RIGHTS FD and one
// kernel SCM_CREDENTIALS matching supervisorPID/uid, then bounded EOF. SO_PASSCRED
// distinguishes even empty extra packets from true EOF. The broker must close
// the bootstrap connection immediately after its response and must authenticate
// the real manager peer before supplying the independent trace FD.
// Request PID fields are observations, never authorization. NewProducer retains
// its original per-event/ACK SCM credential checks against supervisorPID/uid.
func Connect(ctx context.Context, path, stream string, supervisorPID, uid int) (producer *p1trace.Producer, resultErr error) {
	operationContext := ctx
	fail := func() error {
		if operationContext != nil {
			if operationContext.Err() != nil {
				return errors.Join(ErrBootstrap, operationContext.Err())
			}
			if deadline, ok := operationContext.Deadline(); ok && !time.Now().Before(deadline) {
				return errors.Join(ErrBootstrap, context.DeadlineExceeded)
			}
		}
		return ErrBootstrap
	}
	if ctx == nil || ctx.Err() != nil || !validStream(stream) || supervisorPID <= 0 || uid != os.Geteuid() {
		return nil, fail()
	}
	parent, err := privateParent(path, uid)
	if err != nil {
		return nil, fail()
	}
	defer parent.Close()
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	operationContext = bounded
	connection, err := (&net.Dialer{}).DialContext(bounded, "unixpacket", path)
	if err != nil {
		return nil, fail()
	}
	defer connection.Close()
	conn, ok := connection.(*net.UnixConn)
	if !ok {
		return nil, fail()
	}
	deadline, _ := bounded.Deadline()
	if conn.SetDeadline(deadline) != nil {
		return nil, fail()
	}
	stop := context.AfterFunc(bounded, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, fail()
	}
	var peer *syscall.Ucred
	var peerErr error
	if raw.Control(func(fd uintptr) {
		peer, peerErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if peerErr == nil {
			peerErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_PASSCRED, 1)
		}
	}) != nil || peerErr != nil || peer == nil || int(peer.Pid) != supervisorPID || int(peer.Uid) != uid {
		return nil, fail()
	}
	request, err := json.Marshal(Request{1, stream, os.Getpid(), os.Getppid()})
	if err != nil || len(request) > MaxPacket {
		return nil, fail()
	}
	if n, err := conn.Write(request); err != nil || n != len(request) {
		return nil, fail()
	}
	response, fds, eof, err := readPacket(conn, supervisorPID, uid)
	if err != nil || eof {
		return nil, fail()
	}
	defer func() {
		for _, fd := range fds {
			syscall.Close(fd)
		}
	}()
	var reply Response
	if !strict(response, &reply, "version", "stream") || reply.Version != 1 || reply.Stream != stream {
		return nil, fail()
	}
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fds[0]), syscall.F_GETFD, 0)
	if errno != 0 || flags&syscall.FD_CLOEXEC == 0 {
		return nil, fail()
	}
	_, extra, closed, err := readPacket(conn, supervisorPID, uid)
	for _, fd := range extra {
		syscall.Close(fd)
	}
	if err != nil || !closed || bounded.Err() != nil {
		return nil, fail()
	}
	file := os.NewFile(uintptr(fds[0]), "received-supervisor-channel")
	// NewProducer duplicates; the deferred raw FD close still owns this file's
	// descriptor, so detach its finalizer after the constructor returns.
	producer, err = p1trace.NewProducer(file, supervisorPID, uid, stream)
	file.Close()
	fds = nil
	if err != nil {
		return nil, fail()
	}
	if bounded.Err() != nil {
		producer.Close()
		return nil, fail()
	}
	return producer, nil
}
