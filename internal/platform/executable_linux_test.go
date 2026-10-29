//go:build linux && amd64

package platform

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const executableStaticSource = `package main
import("encoding/json";"os";"strconv";"syscall")
func main(){fd,_:=strconv.Atoi(os.Getenv("CHECK_FD"));var st syscall.Stat_t;err:=syscall.Fstat(fd,&st);same:=err==nil&&strconv.FormatUint(st.Ino,10)==os.Getenv("CHECK_INODE");exe,_:=os.Readlink("/proc/self/exe");json.NewEncoder(os.Stdout).Encode(struct{PID int;Exe string;Args []string;PinnedFDInherited bool}{os.Getpid(),exe,os.Args,same})}
`

func executableDigest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func executableStatic(t *testing.T) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	target := filepath.Join(dir, "static-helper")
	if err := os.WriteFile(source, []byte(executableStaticSource), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go", "build", "-trimpath", "-buildvcs=false", "-o", target, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOTOOLCHAIN=local", "GOPROXY=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("static helper compile: %v %s", err, out)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if artifacts := os.Getenv("HOOPA_P1_LAUNCHER_ARTIFACTS"); artifacts != "" {
		if err := os.WriteFile(filepath.Join(artifacts, "static-helper"), raw, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifacts, "static-helper.go.txt"), []byte(executableStaticSource), 0600); err != nil {
			t.Fatal(err)
		}
		record, _ := json.Marshal(struct {
			Args                      []string
			CGO_ENABLED, GOOS, GOARCH string
		}{cmd.Args, "0", "linux", "amd64"})
		if err := os.WriteFile(filepath.Join(artifacts, "static-build-command.json"), record, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("static helper source SHA=%s binary SHA=%s CGO_ENABLED=0", executableDigest([]byte(executableStaticSource)), executableDigest(raw))
	return target, raw
}
func TestExecutableDispatch(t *testing.T) {
	if os.Getenv("HOOPA_EXECUTABLE_FIXTURE") != "1" {
		return
	} // Empty parent dispatcher.
	p, err := OpenExecutable(context.Background(), os.Getenv("HOOPA_EXECUTABLE_PATH"), os.Getenv("HOOPA_EXECUTABLE_SHA"))
	if err != nil {
		t.Fatal(err)
	}
	st := p.info.Sys().(*syscall.Stat_t)
	env := []string{"CHECK_FD=" + strconv.Itoa(int(p.file.Fd())), "CHECK_INODE=" + strconv.FormatUint(st.Ino, 10)}
	if err = p.Exec(context.Background(), []string{p.path, "literal-argv"}, env); err != nil {
		t.Fatal(err)
	}
	t.Fatal("exec returned")
}
func TestExecutablePrimitives(t *testing.T) {
	target, raw := executableStatic(t)
	sha := executableDigest(raw)
	t.Run("actual-same-FD-static-exec", func(t *testing.T) {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, self, "-test.run=^TestExecutableDispatch$", "-test.timeout=4s")
		cmd.Env = append(os.Environ(), "HOOPA_EXECUTABLE_FIXTURE=1", "HOOPA_EXECUTABLE_PATH="+target, "HOOPA_EXECUTABLE_SHA="+sha)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("exec dispatcher: %v %s", err, out)
		}
		var got struct {
			PID               int
			Exe               string
			Args              []string
			PinnedFDInherited bool
		}
		if err = json.Unmarshal(out, &got); err != nil {
			t.Fatal(err)
		}
		if got.PID != cmd.Process.Pid || got.Exe != target || len(got.Args) != 2 || got.Args[0] != target || got.Args[1] != "literal-argv" || got.PinnedFDInherited {
			t.Fatal("same-FD exec identity/argv/CLOEXEC mismatch")
		}
		t.Logf("actual static helper PID=%d Wait exit=%d same-FD executable/argv and CLOEXEC proven", got.PID, cmd.ProcessState.ExitCode())
	})
	for _, name := range []string{"hash", "file-symlink", "ancestor-symlink", "writable-parent", "writable-file", "special-mode", "hardlink", "not-ELF", "wrong-class", "dynamic-interpreter", "oversize", "relative", "canceled"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "helper")
			data := append([]byte(nil), raw...)
			expected := sha
			switch name {
			case "not-ELF":
				data = []byte(strings.Repeat("not executable", 10))
			case "wrong-class":
				data[4] = 1
			case "dynamic-interpreter":
				off := binary.LittleEndian.Uint64(data[32:40])
				binary.LittleEndian.PutUint32(data[off:off+4], 3)
			}
			if err := os.WriteFile(path, data, 0755); err != nil {
				t.Fatal(err)
			}
			expected = executableDigest(data)
			switch name {
			case "hash":
				expected = strings.Repeat("0", 64)
			case "file-symlink":
				link := path + "-link"
				if err := os.Symlink(path, link); err != nil {
					t.Fatal(err)
				}
				path = link
			case "ancestor-symlink":
				link := dir + "-link"
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Remove(link) })
				path = filepath.Join(link, "helper")
			case "writable-parent":
				if err := os.Chmod(dir, 0777); err != nil {
					t.Fatal(err)
				}
			case "writable-file":
				if err := os.Chmod(path, 0775); err != nil {
					t.Fatal(err)
				}
			case "special-mode":
				if err := os.Chmod(path, os.ModeSetuid|0755); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+"-other"); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.Truncate(path, maxExecutableBytes+1); err != nil {
					t.Fatal(err)
				}
			case "relative":
				path = "relative-helper"
			}
			ctx := context.Background()
			if name == "canceled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			p, err := OpenExecutable(ctx, path, expected)
			if p != nil {
				p.Close()
			}
			if !errors.Is(err, ErrExecutableUnverified) {
				t.Fatal("unsafe executable accepted", err)
			}
		})
	}
	t.Run("metadata-change-and-close", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "helper")
		if err := os.WriteFile(path, raw, 0755); err != nil {
			t.Fatal(err)
		}
		p, err := OpenExecutable(context.Background(), path, sha)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if p.verify() != nil {
			t.Fatal("fresh pinned inode rejected")
		}
		if err = os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(p.verify(), ErrExecutableUnverified) {
			t.Fatal("metadata mutation accepted")
		}
		if err = p.Close(); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(p.verify(), ErrExecutableUnverified) {
			t.Fatal("closed pin accepted")
		}
	})
	t.Run("pathname-replacement-preserves-original-rejection", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "helper")
		if err := os.WriteFile(path, raw, 0755); err != nil {
			t.Fatal(err)
		}
		p, err := OpenExecutable(context.Background(), path, sha)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		if err = os.Rename(path, path+"-moved"); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0755); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(p.verify(), ErrExecutableUnverified) {
			t.Fatal("renamed pinned path accepted")
		}
	})
	if p, err := OpenExecutable(nil, target, sha); p != nil || !errors.Is(err, ErrExecutableUnverified) {
		t.Fatal(fmt.Sprint("nil context accepted", err))
	}
}
