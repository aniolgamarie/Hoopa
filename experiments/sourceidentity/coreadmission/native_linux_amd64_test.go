//go:build linux && amd64

package coreadmission

import (
	"errors"
	"syscall"
	"testing"
)

func TestNativeTestRunAttributeAndRefusal(t *testing.T) {
	for _, mode := range []string{"ok", "errno", "retval"} {
		t.Run(mode, func(t *testing.T) {
			b := &nativeBackend{call: func(cmd uint32, attr []byte) error {
				if cmd != 10 || len(attr) != 80 || le.Uint32(attr) != 208 || le.Uint32(attr[8:]) != 64 || le.Uint64(attr[16:]) == 0 || le.Uint32(attr[32:]) != 1 {
					t.Fatal("TEST_RUN layout")
				}
				allowed := map[int]bool{}
				for _, r := range [][2]int{{0, 4}, {8, 12}, {16, 24}, {32, 36}} {
					for i := r[0]; i < r[1]; i++ {
						allowed[i] = true
					}
				}
				for i, v := range attr {
					if !allowed[i] && v != 0 {
						t.Fatalf("nonzero optional/output byte %d", i)
					}
				}
				if mode == "errno" {
					return syscall.EPERM
				}
				if mode == "retval" {
					le.PutUint32(attr[4:], 1)
				}
				return nil
			}}
			err := b.testRun(208)
			if (err == nil) != (mode == "ok") {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeMapAttributesAndErrors(t *testing.T) {
	for _, command := range []uint32{1, 2, 4} {
		t.Run(string(rune('0'+command)), func(t *testing.T) {
			b := &nativeBackend{call: func(cmd uint32, a []byte) error {
				if cmd != command || len(a) != 32 || le.Uint32(a) != 203 || le.Uint32(a[4:]) != 0 || le.Uint64(a[16:]) == 0 {
					t.Fatal("map attr layout")
				}
				if command == 4 {
					if le.Uint64(a[8:]) != 0 {
						t.Fatal("GET_NEXT_KEY must start with NULL")
					}
				} else if le.Uint64(a[8:]) == 0 {
					t.Fatal("missing key")
				}
				flags := uint64(0)
				if command == 1 {
					flags = 4
				}
				if le.Uint64(a[24:]) != flags {
					t.Fatal("map flags")
				}
				return syscall.ENOENT
			}}
			var err error
			switch command {
			case 1:
				_, err = b.lookup(203, make([]byte, 4), 40, 4)
			case 2:
				err = b.update(203, make([]byte, 4), make([]byte, 40))
			case 4:
				err = b.nextKey(203, 4)
			}
			if !errors.Is(err, syscall.ENOENT) {
				t.Fatal(err)
			}
		})
	}
}
func TestNativeSocketNilAndAddressRejection(t *testing.T) {
	b := &nativeBackend{}
	if _, err := b.socket(nil); err == nil {
		t.Fatal("nil socket accepted")
	}
	for _, sa := range []syscall.Sockaddr{&syscall.SockaddrInet6{}, &syscall.SockaddrInet4{Port: 1, Addr: [4]byte{127, 0, 0, 2}}, &syscall.SockaddrInet4{Port: 0, Addr: [4]byte{127, 0, 0, 1}}} {
		if _, err := endpoint(sa); err == nil {
			t.Fatal("invalid endpoint")
		}
	}
	if e, err := endpoint(&syscall.SockaddrInet4{Port: 65535, Addr: [4]byte{127, 0, 0, 1}}); err != nil || e.Port != 65535 {
		t.Fatal(e, err)
	}
}

func TestOriginalSocketSingleControlAndErrorCleanupBoundary(t *testing.T) {
	for _, mode := range []string{"ok", "capture-error", "control-error", "no-callback"} {
		t.Run(mode, func(t *testing.T) {
			calls, reads := 0, 0
			control := func(callback func(uintptr)) error {
				calls++
				if mode != "no-callback" {
					callback(207)
				}
				if mode == "control-error" {
					return syscall.EBADF
				}
				return nil
			}
			capture := func(fd int) (witness, error) {
				reads++
				if fd != 207 {
					t.Fatal("not original callback FD")
				}
				if mode == "capture-error" {
					return witness{}, syscall.ENOPROTOOPT
				}
				return witness{cookie: 7}, nil
			}
			w, err := captureWithControl(control, capture)
			if calls != 1 || reads > 1 {
				t.Fatal("multiple Controls/captures")
			}
			if (err == nil) != (mode == "ok") {
				t.Fatal(err)
			}
			if mode == "ok" && w.cookie != 7 {
				t.Fatal(w)
			}
		})
	}
}
