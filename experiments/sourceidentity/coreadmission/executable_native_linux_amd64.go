//go:build linux && amd64

package coreadmission

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

func (b *nativeBackend) linkTarget(fd int) ([]byte, string, error) {
	first, err := b.info(fd)
	if err != nil || len(first) < 28 {
		return nil, "", errors.Join(err, errors.New("short link info"))
	}
	if le.Uint32(first) != 1 {
		return first, "", nil
	}
	name := make([]byte, 32)
	unpin := pinBuffers(name)
	defer unpin()
	buf := make([]byte, 256)
	le.PutUint64(buf[16:], ptr(name))
	le.PutUint32(buf[24:], uint32(len(name)))
	second, err := b.readInfo(fd, buf)
	runtime.KeepAlive(name)
	if err != nil {
		return nil, "", err
	}
	if len(second) < 28 || !bytes.Equal(first[:12], second[:12]) {
		return nil, "", errors.New("raw tracepoint link changed")
	}
	n := le.Uint32(second[24:])
	if n < 2 || n > uint32(len(name)) || name[n-1] != 0 || bytes.IndexByte(name[:n-1], 0) >= 0 {
		return nil, "", errors.New("raw tracepoint target string")
	}
	return second, string(name[:n-1]), nil
}

// Resolve the actual program target BTF object, rather than equating unrelated
// function IDs. Only this fixed vmlinux byte stream is accepted by the prototype.
func (b *nativeBackend) targetBTF(id uint32) (result error) {
	attr := make([]byte, 12)
	le.PutUint32(attr, id)
	fd, _, errno := syscall.Syscall(321, 19, uintptr(unsafe.Pointer(&attr[0])), uintptr(len(attr)))
	runtime.KeepAlive(attr)
	if errno != 0 {
		return errno
	}
	defer func() { result = errors.Join(result, syscall.Close(int(fd))) }()
	first, err := b.info(int(fd))
	if err != nil {
		return err
	}
	if len(first) < 32 || le.Uint32(first[8:]) != executableBTFSize || le.Uint32(first[12:]) != id || le.Uint32(first[28:]) != 1 {
		return errors.New("target BTF declaration mismatch")
	}
	raw := make([]byte, executableBTFSize)
	name := make([]byte, 32)
	unpin := pinBuffers(raw, name)
	defer unpin()
	info := make([]byte, 256)
	le.PutUint64(info, ptr(raw))
	le.PutUint32(info[8:], uint32(len(raw)))
	le.PutUint64(info[16:], ptr(name))
	le.PutUint32(info[24:], uint32(len(name)))
	second, err := b.readInfo(int(fd), info)
	runtime.KeepAlive(raw)
	runtime.KeepAlive(name)
	if err != nil {
		return err
	}
	if len(second) < 32 || le.Uint32(second[8:]) != executableBTFSize || le.Uint32(second[12:]) != id || le.Uint32(second[28:]) != 1 || !bytes.Equal(name[:8], []byte("vmlinux\x00")) || fmt.Sprintf("%x", sha256.Sum256(raw)) != executableBTFSHA256 {
		return errors.New("target BTF content mismatch")
	}
	return nil
}
