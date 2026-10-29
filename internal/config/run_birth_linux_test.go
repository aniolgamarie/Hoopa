//go:build linux

package config

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func runBirthFixture(t *testing.T) (*Store, RunIntent, string, *PreparedRun, *os.Root, RunBirth) {
	t.Helper()
	_, s, intent, nonce := runStoreFixture(t)
	ticket, dir := runStorePrepare(t, s, intent)
	// Deliberately synthetic boot/start: this exercises storage, never exec.
	birth := RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: ticket.Digest(), PID: os.Getpid(), UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
	return s, intent, nonce, ticket, dir, birth
}
func TestRunBirthConcurrentExclusiveClaim(t *testing.T) {
	s, intent, nonce, ticket, dir, birth := runBirthFixture(t)
	before, sha, err := s.ReadRunIntent(intent.RunID)
	if err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-gate; results <- ClaimRunBirth(dir, ticket.Digest(), nonce, birth) }()
	}
	close(gate)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, ErrRunConflict) {
			conflict++
		} else {
			t.Fatal("unexpected claim error:", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("claims not exclusive:", success, conflict)
	}
	info, err := dir.Stat("birth.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.Sys().(*syscall.Stat_t).Nlink != 1 || info.Mode().Perm() != 0600 {
		t.Fatal("birth has unsafe final metadata")
	}
	again, againSHA, err := s.ReadRunIntent(intent.RunID)
	if err != nil || !reflect.DeepEqual(before, again) || sha != againSHA {
		t.Fatal("claim changed original intent")
	}
	if err := ClaimRunBirth(dir, ticket.Digest(), nonce, birth); !errors.Is(err, ErrRunConflict) {
		t.Fatal("repeat birth granted exec claim:", err)
	}
}
func TestRunBirthDelegatedValidation(t *testing.T) {
	for _, name := range []string{"wrong-nonce", "wrong-pid", "wrong-uid", "wrong-intent-sha", "wrong-run"} {
		t.Run(name, func(t *testing.T) {
			s, intent, nonce, ticket, dir, birth := runBirthFixture(t)
			expected := ticket.Digest()
			switch name {
			case "wrong-nonce":
				nonce = strings.Repeat("cd", 32)
			case "wrong-pid":
				birth.PID++
			case "wrong-uid":
				birth.UID++
			case "wrong-intent-sha":
				expected = digest([]byte("other-intent"))
			case "wrong-run":
				birth.RunID = "other-run"
			}
			if err := ClaimRunBirth(dir, expected, nonce, birth); err == nil {
				t.Fatal("unbound delegated claim accepted")
			}
			if _, _, err := s.ReadRunBirth(intent.RunID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid claim published birth:", err)
			}
		})
	}
}
func TestRunBirthFailureBoundaries(t *testing.T) {
	for _, point := range []string{"birth.file.write", "birth.file.sync", "birth.file.closed", "birth.link", "birth.unlink-temp", "birth.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			s, intent, nonce, ticket, dir, birth := runBirthFixture(t)
			active, _ := s.ReadActive()
			injected := errors.New("birth boundary failure")
			hit := 0
			writes := &Store{fault: func(at string) error {
				if at == point {
					hit++
					return injected
				}
				return nil
			}}
			err := claimRunBirth(dir, ticket.Digest(), nonce, birth, writes)
			uncertain := point == "birth.unlink-temp" || point == "birth.parent-sync"
			if !errors.Is(err, injected) || hit != 1 || errors.Is(err, ErrUncertain) != uncertain || writes.poisoned != uncertain {
				t.Fatalf("birth error lost %v hit=%d poisoned=%v", err, hit, writes.poisoned)
			}
			runStoreActiveUnchanged(t, s, active)
			_, _, readErr := s.ReadRunBirth(intent.RunID)
			switch point {
			case "birth.unlink-temp":
				info, err := dir.Stat("birth.json")
				if err != nil {
					t.Fatal(err)
				}
				if info.Sys().(*syscall.Stat_t).Nlink != 2 || !errors.Is(readErr, ErrUnsafePath) {
					t.Fatal("unfinished dual-link birth was trusted:", readErr)
				}
				if err := ClaimRunBirth(dir, ticket.Digest(), nonce, birth); !errors.Is(err, ErrRunConflict) {
					t.Fatal("partial claim retried:", err)
				}
			case "birth.parent-sync":
				if readErr != nil {
					t.Fatal("published single-link birth unreadable:", readErr)
				}
				if err := ClaimRunBirth(dir, ticket.Digest(), nonce, birth); !errors.Is(err, ErrRunConflict) {
					t.Fatal("uncertain claim retried:", err)
				}
			default:
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal("pre-link birth published:", readErr)
				}
				// Storage-only retry demonstrates no published slot; no test executes.
				if err := ClaimRunBirth(dir, ticket.Digest(), nonce, birth); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
