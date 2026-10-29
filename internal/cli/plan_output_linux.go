//go:build linux && amd64

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"hoopa/internal/control"
)

var errPlanOutput = errors.New("plan output publication unavailable")

// writePlanOutput publishes only a new caller-selected file. This directory is
// separate from Store authority: its final inode is anchored and checked, but
// its ancestors need not satisfy the stricter private Store traversal policy.
func writePlanOutput(ctx context.Context, path string, raw []byte) error {
	return writePlanOutputChecked(ctx, path, raw, nil)
}

func writePlanOutputChecked(ctx context.Context, path string, raw []byte, fault func(string) error) error {
	if ctx == nil || path == "" || strings.ContainsRune(path, 0) || len(raw) == 0 || len(raw) > control.MaxRequestBytes {
		return control.ErrRequest
	}
	check := func(stage string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fault != nil && fault(stage) != nil {
			return errPlanOutput
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	base := filepath.Base(path)
	if base == "." || base == ".." || base == "/" {
		return control.ErrRequest
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return control.ErrRequest
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return errPlanOutput
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return errPlanOutput
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	// A sticky directory owned by root or this user protects our temporary
	// name too. Other group/other-writable directories cannot protect publication.
	if !ok || !info.IsDir() || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 && st.Mode&syscall.S_ISVTX == 0 {
		return control.ErrRequest
	}
	if _, err = root.Lstat(base); err == nil {
		return control.ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return errPlanOutput
	}
	token, err := randomHex(24)
	if err != nil {
		return errPlanOutput
	}
	tmp := ".hoopa-plan-" + token
	file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errPlanOutput
	}
	tempRemaining := true
	defer func() {
		if file != nil {
			file.Close()
		}
		if tempRemaining {
			root.Remove(tmp)
		}
	}()
	if err = file.Chmod(0600); err != nil {
		return errPlanOutput
	}
	if err = check("write"); err != nil {
		return err
	}
	if n, err := file.Write(raw); err != nil || n != len(raw) {
		return errPlanOutput
	}
	if err = check("file-sync"); err != nil {
		return err
	}
	if err = file.Sync(); err != nil {
		return errPlanOutput
	}
	err = file.Close()
	file = nil
	if err != nil {
		return errPlanOutput
	}
	if err = check("publish"); err != nil {
		return err
	}
	// Link creates the complete destination atomically and never replaces an
	// existing name. Both names are in the same anchored controlled directory.
	if err = root.Link(tmp, base); err != nil {
		if errors.Is(err, os.ErrExist) {
			return control.ErrConflict
		}
		return errPlanOutput
	}
	// After publication, failures retain the complete caller output. They must
	// not remove it or imply that no file was created.
	if err = root.Remove(tmp); err != nil {
		return errPlanOutput
	}
	tempRemaining = false
	if err = check("directory-sync"); err != nil {
		return err
	}
	if err = dir.Sync(); err != nil {
		return errPlanOutput
	}
	return nil
}
