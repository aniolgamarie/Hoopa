//go:build linux

package sourceidentity

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type VisibilityObserver struct {
	PID        int    `json:"pid"`
	StartTicks string `json:"start_ticks"`
	NetNS      string `json:"netns"`
	PIDNS      string `json:"pidns"`
	UID        uint32 `json:"uid"`
}
type VisibilityCounts struct {
	Enumerated       int `json:"enumerated"`
	SameUID          int `json:"same_uid"`
	Readable         int `json:"readable"`
	PermissionDenied int `json:"permission_denied"`
	Races            int `json:"exit_or_fd_races"`
	OtherErrors      int `json:"other_errors"`
	FDRead           int `json:"fd_read"`
}
type VisibilityBlocker struct {
	PID        int    `json:"pid"`
	StartTicks string `json:"start_ticks,omitempty"`
	Stage      string `json:"stage"`
	Errno      int    `json:"errno"`
}
type VisibilityLimitation struct {
	Reason string `json:"reason"`
	Status string `json:"status"`
}

// VisibilityReport describes only a bounded, non-atomic visibility scan.
// Passed never proves unique socket ownership or qualifies G01.
type VisibilityReport struct {
	SchemaVersion int                    `json:"schema_version"`
	Observer      VisibilityObserver     `json:"observer"`
	Status        string                 `json:"status"`
	Reason        string                 `json:"reason"`
	Counts        VisibilityCounts       `json:"counts"`
	Blockers      []VisibilityBlocker    `json:"blockers"`
	Limitations   []VisibilityLimitation `json:"limitations"`
}

type visibilityProc interface {
	list(string, int) ([]string, bool, error)
	read(string) ([]byte, error)
	link(string) (string, error)
	closeDir() error
}
type realVisibilityProc struct{ dir *os.File }

func (p *realVisibilityProc) closeDir() error {
	if p.dir == nil {
		return nil
	}
	f := p.dir
	p.dir = nil
	return f.Close()
}
func (p *realVisibilityProc) list(path string, limit int) ([]string, bool, error) {
	if err := p.closeDir(); err != nil {
		return nil, false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	// Keep the listed FD directory open until its links have been checked.
	// Closing it immediately would manufacture an ENOENT in self's FD set.
	p.dir = f
	n, err := f.Readdirnames(limit + 1)
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if len(n) > limit {
		return n[:limit], true, err
	}
	return n, false, err
}
func (realVisibilityProc) read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	if len(b) > 65536 {
		return nil, syscall.EOVERFLOW
	}
	return b, err
}
func (realVisibilityProc) link(path string) (string, error) { return os.Readlink(path) }

// InspectVisibility reads /proc without privilege changes or socket activity.
// Limits are 4096 proc entries, 4096 FDs per process and 65536 FDs overall.
// Truncation and cancellation never pass. Cross-UID sharing, FD transfer and
// the non-atomic owner set remain explicitly unverified even on a clean scan.
func InspectVisibility(ctx context.Context) VisibilityReport {
	return inspectVisibility(ctx, &realVisibilityProc{}, os.Getpid(), uint32(os.Geteuid()))
}

func visibilityStart(raw []byte, pid int) (string, error) {
	s := string(raw)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 1 || r <= l || strings.TrimSpace(s[:l]) != strconv.Itoa(pid) {
		return "", syscall.EINVAL
	}
	f := strings.Fields(s[r+1:])
	if len(f) < 20 {
		return "", syscall.EINVAL
	}
	if _, err := strconv.ParseUint(f[19], 10, 64); err != nil {
		return "", syscall.EINVAL
	}
	return f[19], nil
}
func visibilityUID(raw []byte) (uint32, error) {
	found := false
	var uid uint32
	for _, l := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(l, "Uid:") {
			continue
		}
		if found {
			return 0, syscall.EINVAL
		}
		found = true
		f := strings.Fields(l)
		if len(f) != 5 {
			return 0, syscall.EINVAL
		}
		for i := 1; i < 5; i++ {
			n, err := strconv.ParseUint(f[i], 10, 32)
			if err != nil {
				return 0, syscall.EINVAL
			}
			if i == 2 {
				uid = uint32(n)
			}
		}
	}
	if !found {
		return 0, syscall.EINVAL
	}
	return uid, nil
}

func inspectVisibility(ctx context.Context, fs visibilityProc, pid int, uid uint32) VisibilityReport {
	defer fs.closeDir()
	r := VisibilityReport{SchemaVersion: 1, Observer: VisibilityObserver{PID: pid, UID: uid}, Status: "passed", Reason: "scan_readable", Blockers: []VisibilityBlocker{}}
	for _, s := range []string{"cross_uid_fd_sharing", "fd_transfer_races", "non_atomic_owner_set"} {
		r.Limitations = append(r.Limitations, VisibilityLimitation{s, "unverified"})
	}
	block := func(p int, start, stage string, err error) {
		var errno syscall.Errno
		if !errors.As(err, &errno) {
			errno = syscall.EIO
		}
		status, reason := "unverified", "metadata_unavailable"
		switch {
		case errors.Is(err, os.ErrPermission):
			r.Counts.PermissionDenied++
			reason = "owner_visibility"
			if stage != "uid" {
				status = "failed"
			}
		case errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ESTALE):
			r.Counts.Races++
			reason = "process_or_fd_changed"
		default:
			r.Counts.OtherErrors++
		}
		if r.Status == "passed" || status == "failed" && r.Status != "failed" {
			r.Status = status
			r.Reason = reason
		}
		if len(r.Blockers) < 16 {
			r.Blockers = append(r.Blockers, VisibilityBlocker{p, start, stage, int(errno)})
		}
	}
	stop := func(reason string) VisibilityReport { r.Status = "unverified"; r.Reason = reason; return r }
	identity := func(p int) (string, error) {
		b, e := fs.read(filepath.Join("/proc", strconv.Itoa(p), "stat"))
		if e != nil {
			return "", e
		}
		return visibilityStart(b, p)
	}
	euid := func(p int) (uint32, error) {
		b, e := fs.read(filepath.Join("/proc", strconv.Itoa(p), "status"))
		if e != nil {
			return 0, e
		}
		return visibilityUID(b)
	}
	if ctx.Err() != nil {
		return stop("cancelled")
	}
	var err error
	r.Observer.StartTicks, err = identity(pid)
	if err != nil {
		block(pid, "", "stat", err)
		return r
	}
	for _, ns := range []string{"net", "pid"} {
		v, e := fs.link(filepath.Join("/proc", strconv.Itoa(pid), "ns", ns))
		if e != nil || v == "" {
			if e == nil {
				e = syscall.EINVAL
			}
			block(pid, r.Observer.StartTicks, "namespace", e)
			return r
		}
		if ns == "net" {
			r.Observer.NetNS = v
		} else {
			r.Observer.PIDNS = v
		}
	}
	names, truncated, err := fs.list("/proc", 4096)
	if err != nil {
		block(0, "", "process_listing", err)
		return r
	}
	if truncated {
		return stop("process_limit")
	}
	totalFD := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return stop("cancelled")
		}
		p, e := strconv.Atoi(name)
		if e != nil || p <= 0 {
			continue
		}
		r.Counts.Enumerated++
		u, e := euid(p)
		if e != nil {
			block(p, "", "uid", e)
			continue
		}
		if u != uid {
			after, e := euid(p)
			if e != nil {
				block(p, "", "uid", e)
			} else if after != u {
				block(p, "", "uid_changed", syscall.ESTALE)
			}
			continue
		}
		r.Counts.SameUID++
		start, e := identity(p)
		if e != nil {
			block(p, "", "stat", e)
			continue
		}
		if p == pid && start != r.Observer.StartTicks {
			block(p, start, "identity_changed", syscall.ESTALE)
			continue
		}
		fds, limit, e := fs.list(filepath.Join("/proc", name, "fd"), 4096)
		if e != nil {
			block(p, start, "fd_listing", e)
			continue
		}
		if limit {
			return stop("descriptor_limit")
		}
		clean := true
		for _, fd := range fds {
			if ctx.Err() != nil {
				return stop("cancelled")
			}
			if totalFD >= 65536 {
				return stop("descriptor_limit")
			}
			totalFD++
			_, e = fs.link(filepath.Join("/proc", name, "fd", fd))
			if e != nil {
				block(p, start, "fd_readlink", e)
				clean = false
			} else {
				r.Counts.FDRead++
			}
		}
		after, e := identity(p)
		if e != nil {
			block(p, start, "stat_recheck", e)
			clean = false
		} else if after != start {
			block(p, start, "identity_changed", syscall.ESTALE)
			clean = false
		}
		u, e = euid(p)
		if e != nil {
			block(p, start, "uid", e)
			clean = false
		} else if u != uid {
			block(p, start, "uid_changed", syscall.ESTALE)
			clean = false
		}
		if e := fs.closeDir(); e != nil {
			block(p, start, "fd_close", e)
			clean = false
		}
		if clean {
			r.Counts.Readable++
		}
	}
	if e := fs.closeDir(); e != nil {
		block(0, "", "directory_close", e)
	}
	if ctx.Err() != nil {
		return stop("cancelled")
	}
	return r
}
