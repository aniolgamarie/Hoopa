//go:build linux

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var ErrUnsafePath = errors.New("UNSAFE_STORE_PATH")

func privateInfo(info os.FileInfo, directory bool) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0600)
	if directory {
		mode = 0700
	}
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != mode || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 || info.IsDir() != directory || !directory && (!info.Mode().IsRegular() || st.Nlink != 1) {
		return fmt.Errorf("%w: %s", ErrUnsafePath, info.Name())
	}
	return nil
}

// Traverse each component with openat/O_NOFOLLOW. Existing ancestors must be
// controlled by root or this uid. A root-owned sticky /tmp is allowed; arbitrary
// group/other writable ancestors are not. Same-uid hostile processes and mount
// replacement require a separate isolation boundary; this store is not one.
func privateRoot(path string, create bool) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
		return nil, ErrUnsafePath
	}
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { syscall.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(filepath.Clean(path), "/"), "/")
	for i, part := range parts {
		made := false
		next, e := syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if errors.Is(e, syscall.ENOENT) && create {
			e = syscall.Mkdirat(fd, part, 0700)
			made = e == nil
			if e == nil || errors.Is(e, syscall.EEXIST) {
				next, e = syscall.Openat(fd, part, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
			}
		}
		if e != nil {
			return nil, fmt.Errorf("open private directory %s: %w", part, e)
		}
		var st syscall.Stat_t
		e = syscall.Fstat(next, &st)
		if e == nil {
			stickyRoot := st.Uid == 0 && st.Mode&syscall.S_ISVTX != 0
			if st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && !stickyRoot || (i == len(parts)-1 || made) && (st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != 0700) {
				e = ErrUnsafePath
			}
		}
		if e == nil && made {
			e = syscall.Fsync(fd)
		}
		if e != nil {
			syscall.Close(next)
			return nil, fmt.Errorf("private directory %s: %w", part, e)
		}
		syscall.Close(fd)
		fd = next
	}
	// OpenRoot on a proc fd retains the inode selected above, including across
	// ancestor renames; it does not repeat pathname traversal.
	return os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd))
}

func openPrivateDir(root *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		err := root.Mkdir(name, 0700)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if err == nil {
			f, e := root.Open(".")
			if e != nil {
				return nil, e
			}
			if e = errors.Join(f.Sync(), f.Close()); e != nil {
				return nil, e
			}
		}
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err = privateInfo(info, true); err != nil {
		return nil, err
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		child.Close()
		return nil, errors.Join(ErrUnsafePath, err)
	}
	return child, nil
}

func openPrivateFile(root *os.Root, name string, flags int) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err = privateInfo(before, false); err != nil {
		return nil, err
	}
	f, err := root.OpenFile(name, flags|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, errors.Join(ErrUnsafePath, err)
	}
	if err = privateInfo(after, false); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func lockWriter(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(".writer.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		file, err = openPrivateFile(root, ".writer.lock", os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		err = privateInfo(info, false)
	}
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = ErrWriterBusy
		}
	}
	if err == nil {
		err = file.Sync()
	}
	if err == nil {
		parent, e := root.Open(".")
		if e != nil {
			err = e
		} else {
			err = errors.Join(parent.Sync(), parent.Close())
		}
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}
