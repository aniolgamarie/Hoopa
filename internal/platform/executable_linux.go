//go:build linux && amd64

package platform

import (
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var ErrExecutableUnverified = errors.New("EXECUTABLE_UNVERIFIED")

const maxExecutableBytes = 128 << 20

// PinnedExecutable retains the exact opened inode whose bytes were verified.
// It is for a single dedicated launcher, not concurrent use or serialized rights.
// File contents must remain protected by ownership/permissions; this is not an
// isolation boundary against hostile same-uid/root processes or mount changes.
type PinnedExecutable struct {
	file *os.File
	path string
	info os.FileInfo
}

// OpenExecutable never follows user-selected symlinks, including ancestors.
// It opens no writable descriptors and does not execute the file.
func OpenExecutable(ctx context.Context, path, expectedSHA string) (*PinnedExecutable, error) {
	if ctx == nil || ctx.Err() != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || len(path) > 4096 || strings.ContainsRune(path, 0) || !validProcessDigest(expectedSHA) {
		return nil, ErrExecutableUnverified
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrExecutableUnverified
	}
	defer func() { syscall.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for _, part := range parts[:len(parts)-1] {
		next, err := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, ErrExecutableUnverified
		}
		var st syscall.Stat_t
		err = syscall.Fstat(next, &st)
		stickyRoot := st.Uid == 0 && st.Mode&syscall.S_ISVTX != 0
		if err != nil || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && !stickyRoot {
			syscall.Close(next)
			return nil, ErrExecutableUnverified
		}
		syscall.Close(fd)
		fd = next
	}
	binaryFD, err := syscall.Openat(fd, parts[len(parts)-1], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrExecutableUnverified
	}
	file := os.NewFile(uintptr(binaryFD), path)
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	before, err := file.Stat()
	if err != nil || executableInfo(before) != nil {
		return nil, ErrExecutableUnverified
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(file, maxExecutableBytes+1)); err != nil || hex.EncodeToString(hash.Sum(nil)) != expectedSHA || ctx.Err() != nil {
		return nil, ErrExecutableUnverified
	}
	parsed, err := elf.NewFile(file)
	if err != nil || parsed.Class != elf.ELFCLASS64 || parsed.Data != elf.ELFDATA2LSB || parsed.Machine != elf.EM_X86_64 || parsed.Type != elf.ET_EXEC && parsed.Type != elf.ET_DYN {
		return nil, ErrExecutableUnverified
	}
	// The fixed basic core is built without a dynamic interpreter. Its checksum
	// therefore does not silently delegate execution to an unpinned ELF loader.
	for _, prog := range parsed.Progs {
		if prog.Type == elf.PT_INTERP {
			return nil, ErrExecutableUnverified
		}
	}
	pinned := &PinnedExecutable{file: file, path: path, info: before}
	if pinned.verify() != nil || ctx.Err() != nil {
		return nil, ErrExecutableUnverified
	}
	keep = true
	return pinned, nil
}
func executableInfo(info os.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Size() < 64 || info.Size() > maxExecutableBytes || st.Nlink != 1 || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&07022 != 0 || st.Mode&0111 == 0 {
		return ErrExecutableUnverified
	}
	return nil
}
func (p *PinnedExecutable) verify() error {
	if p == nil || p.file == nil {
		return ErrExecutableUnverified
	}
	now, err := p.file.Stat()
	if err != nil || executableInfo(now) != nil || !os.SameFile(p.info, now) || p.info.Size() != now.Size() || p.info.ModTime() != now.ModTime() || p.info.Mode() != now.Mode() {
		return ErrExecutableUnverified
	}
	oldStat := p.info.Sys().(*syscall.Stat_t)
	newStat := now.Sys().(*syscall.Stat_t)
	if oldStat.Ctim != newStat.Ctim || oldStat.Uid != newStat.Uid || oldStat.Gid != newStat.Gid {
		return ErrExecutableUnverified
	}
	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", p.file.Fd()))
	if err != nil || target != p.path {
		return ErrExecutableUnverified
	}
	return nil
}
func (p *PinnedExecutable) Close() error {
	if p == nil || p.file == nil {
		return nil
	}
	err := p.file.Close()
	p.file = nil
	return err
}

// Exec replaces this dedicated launcher process; success never returns. argv[0]
// retains the expected real path, while the kernel selects the verified inode
// through procfs. The CLOEXEC descriptor closes upon a successful ELF exec.
func (p *PinnedExecutable) Exec(ctx context.Context, argv, environment []string) error {
	if ctx == nil || ctx.Err() != nil || p == nil || !launchArguments(p.path, argv) || p.verify() != nil || ctx.Err() != nil {
		return ErrExecutableUnverified
	}
	return errors.Join(ErrExecutableUnverified, syscall.Exec(fmt.Sprintf("/proc/self/fd/%d", p.file.Fd()), argv, environment))
}
