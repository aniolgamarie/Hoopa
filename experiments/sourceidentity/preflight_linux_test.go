//go:build linux

package sourceidentity

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"testing"
)

type preflightFake struct {
	names  map[string][]string
	data   map[string]string
	uids   map[string]uint32
	errs   map[string]error
	links  int
	reads  map[string]int
	second map[string]string
}

func (f *preflightFake) list(path string, limit int) ([]string, bool, error) {
	if err := f.errs[path]; err != nil {
		return nil, false, err
	}
	n := f.names[path]
	if len(n) > limit {
		return n[:limit], true, nil
	}
	return n, false, nil
}
func (f *preflightFake) read(path string) ([]byte, error) {
	if f.reads == nil {
		f.reads = map[string]int{}
	}
	f.reads[path]++
	if v, ok := f.second[path]; ok && f.reads[path] > 1 {
		return []byte(v), f.errs[path]
	}
	return []byte(f.data[path]), f.errs[path]
}
func (f *preflightFake) closeDir() error { return nil }
func (f *preflightFake) link(path string) (string, error) {
	f.links++
	if err := f.errs[path]; err != nil {
		return "", err
	}
	if strings.Contains(path, "/fd/") {
		return "SECRET_FD_TARGET", nil
	}
	return "namespace:[1]", nil
}
func (f *preflightFake) uid(path string) (uint32, error) { return f.uids[path], f.errs[path+"/uid"] }

func preflightStat(pid int, start string) string {
	return fmt.Sprintf("%d (name with ) spaces) S %s %s", pid, strings.Repeat("0 ", 18), start)
}
func preflightFixture() *preflightFake {
	return &preflightFake{
		names: map[string][]string{"/proc": {"10", "20", "sys"}, "/proc/10/fd": {"0", "1"}},
		data:  map[string]string{"/proc/10/stat": preflightStat(10, "123"), "/proc/10/status": "Uid:\t1000\t1000\t1000\t1000\n", "/proc/20/status": "Uid:\t2000\t2000\t2000\t2000\n"},
		uids:  map[string]uint32{"/proc/10": 1000, "/proc/20": 2000}, errs: map[string]error{},
	}
}

func TestPreflightReadableScopeAndRedaction(t *testing.T) {
	f := preflightFixture()
	r := inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status != "passed" || r.Counts.SameUID != 1 || r.Counts.Readable != 1 || r.Counts.FDRead != 2 {
		t.Fatalf("unexpected scan: %+v", r)
	}
	if r.Observer.StartTicks != "123" || r.Observer.NetNS == "" || r.Observer.PIDNS == "" || len(r.Limitations) != 3 {
		t.Fatal("missing scope evidence")
	}
	b, err := json.Marshal(r)
	if err != nil || strings.Contains(string(b), "SECRET") || strings.Contains(string(b), "name with") {
		t.Fatal("private proc contents leaked")
	}
	for _, l := range r.Limitations {
		if l.Status != "unverified" {
			t.Fatal("owner guarantee inferred")
		}
	}
}

func TestPreflightErrorsNeverPass(t *testing.T) {
	for _, tc := range []struct {
		path   string
		err    error
		status string
		count  string
	}{
		{"/proc/10/fd", syscall.EACCES, "failed", "permission"},
		{"/proc/10/fd/1", syscall.EPERM, "failed", "permission"},
		{"/proc/10/fd/1", syscall.ENOENT, "unverified", "race"},
		{"/proc/20/status", syscall.EIO, "unverified", "other"},
		{"/proc/20/status", syscall.EACCES, "unverified", "permission"},
		{"/proc/10/stat", syscall.EIO, "unverified", "other"},
		{"/proc/10/ns/pid", syscall.EACCES, "failed", "permission"},
	} {
		t.Run(tc.path+tc.err.Error(), func(t *testing.T) {
			f := preflightFixture()
			f.errs[tc.path] = tc.err
			r := inspectVisibility(context.Background(), f, 10, 1000)
			if r.Status != tc.status || len(r.Blockers) == 0 {
				t.Fatalf("silent error: %+v", r)
			}
			if tc.count == "permission" && r.Counts.PermissionDenied == 0 || tc.count == "race" && r.Counts.Races == 0 || tc.count == "other" && r.Counts.OtherErrors == 0 {
				t.Fatal("error not counted")
			}
		})
	}
}

func TestPreflightEffectiveUIDNotDirectoryOwnerAndBookends(t *testing.T) {
	f := preflightFixture()
	// Directory owner disagrees in both directions; only status EUID counts.
	f.uids["/proc/10"] = 0
	f.uids["/proc/20"] = 1000
	r := inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status != "passed" || r.Counts.SameUID != 1 {
		t.Fatal("directory ownership used as EUID")
	}
	f = preflightFixture()
	f.second = map[string]string{"/proc/10/status": "Uid: 1000 0 1000 1000\n"}
	r = inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status == "passed" || r.Counts.Races != 1 {
		t.Fatal("EUID change ignored")
	}
	f = preflightFixture()
	f.second = map[string]string{"/proc/10/stat": preflightStat(10, "124")}
	r = inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status == "passed" || r.Counts.Races != 1 {
		t.Fatal("PID reuse ignored")
	}
}

func TestPreflightBoundsAndCancellation(t *testing.T) {
	f := preflightFixture()
	for i := 0; i < 4100; i++ {
		f.names["/proc"] = append(f.names["/proc"], fmt.Sprint(i+100))
	}
	r := inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status == "passed" || r.Reason != "process_limit" {
		t.Fatalf("truncated listing passed: %+v", r)
	}
	f = preflightFixture()
	f.names["/proc/10/fd"] = make([]string, 4097)
	r = inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status == "passed" || r.Reason != "descriptor_limit" {
		t.Fatal("descriptor truncation passed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = inspectVisibility(ctx, preflightFixture(), 10, 1000)
	if r.Status != "unverified" || r.Reason != "cancelled" {
		t.Fatal("cancelled scan passed")
	}
}

func TestPreflightMalformedIdentityAndBoundedBlockers(t *testing.T) {
	f := preflightFixture()
	f.data["/proc/10/stat"] = "10 (bad) S 0"
	r := inspectVisibility(context.Background(), f, 10, 1000)
	if r.Status == "passed" {
		t.Fatal("malformed stat accepted")
	}
	f = preflightFixture()
	for i := 0; i < 40; i++ {
		name := fmt.Sprint(i + 100)
		f.names["/proc/10/fd"] = append(f.names["/proc/10/fd"], name)
		f.errs["/proc/10/fd/"+name] = syscall.EACCES
	}
	r = inspectVisibility(context.Background(), f, 10, 1000)
	if len(r.Blockers) != 16 || r.Counts.PermissionDenied != 40 {
		t.Fatal("blockers or error counts unbounded/incomplete")
	}
}

func TestPreflightStatusUIDParsing(t *testing.T) {
	uid, err := visibilityUID([]byte("Name:\tPRIVATE\nUid:\t2000\t1000\t3000\t4000\n"))
	if err != nil || uid != 1000 {
		t.Fatal("effective UID field not used")
	}
	for _, raw := range []string{"", "Uid: 1000 1000 1000", "Uid: 1 -1 1 1", "Uid: 1 4294967296 1 1", "Uid: 1 1 1 1\nUid: 1 1 1 1"} {
		if _, err := visibilityUID([]byte(raw)); err == nil {
			t.Fatal("invalid UID metadata accepted")
		}
	}
}
