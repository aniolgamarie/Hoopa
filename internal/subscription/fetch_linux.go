//go:build linux

package subscription

import (
	"context"
	"errors"
	"os"
	"syscall"
)

func readFetchFile(ctx context.Context, path string, limit int, secret bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// O_NONBLOCK prevents a supplied FIFO from hanging before regular-file
	// validation. The final component must not be a symlink.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrFetchSource
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() {
		return nil, ErrFetchSource
	}
	st, ok := before.Sys().(*syscall.Stat_t)
	if !ok || secret && (st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 || before.Mode().Perm()&0077 != 0 || before.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0) {
		return nil, ErrFetchSource
	}
	if before.Size() < 0 || before.Size() > int64(limit) {
		return nil, ErrInputLimit
	}
	raw, err := fetchRead(ctx, f, limit)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || int64(len(raw)) != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		return nil, ErrFetch
	}
	end, ok := after.Sys().(*syscall.Stat_t)
	if !ok || st.Ctim != end.Ctim {
		return nil, ErrFetch
	}
	if err := errors.Join(ctx.Err(), f.Close()); err != nil {
		return nil, ErrFetch
	}
	return raw, nil
}
