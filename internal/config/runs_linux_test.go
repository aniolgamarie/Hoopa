//go:build linux

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// All run-store fixtures are synthetic storage records, not launched cores.
func runStoreFixture(t *testing.T) (Paths, *Store, RunIntent, string) {
	t.Helper()
	p := storePaths(t)
	s := openWriter(t, p)
	m, files := fixtureBundle()
	sha, err := s.PublishBundle(m, files)
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Repeat("ab", 32)
	intent := RunIntent{Version: 1, RunID: "storage-run", OperationID: "birth-operation", Generation: m.Generation, ManifestSHA256: sha, Binary: m.Binaries[0], Workdir: "/synthetic/work", Arguments: []string{m.Binaries[0].Path, "-d", "/synthetic/work"}, Controller: "127.0.0.1:12345", ConfigSHA256: digest(files["core.yaml"]), NonceSHA256: digest([]byte(nonce))}
	return p, s, intent, nonce
}
func runStorePrepare(t *testing.T, s *Store, intent RunIntent) (*PreparedRun, *os.Root) {
	t.Helper()
	ticket, err := s.PrepareRun(intent)
	if err != nil {
		t.Fatal(err)
	}
	fd, err := ticket.TakeBirthDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	dir, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", fd.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dir.Close() })
	return ticket, dir
}
func runStoreBirth(t *testing.T, s *Store, intent RunIntent, nonce string) (RunBirth, string, *os.Root) {
	t.Helper()
	ticket, dir := runStorePrepare(t, s, intent)
	// Boot/start are deliberately synthetic; Claim only binds self PID/UID/nonce.
	birth := RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: ticket.Digest(), PID: os.Getpid(), UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
	if err := ClaimRunBirth(dir, ticket.Digest(), nonce, birth); err != nil {
		t.Fatal(err)
	}
	got, sha, err := s.ReadRunBirth(intent.RunID)
	if err != nil || got != birth {
		t.Fatal("birth read:", err)
	}
	return birth, sha, dir
}
func runStoreRegistration(intent RunIntent, birth RunBirth, birthSHA string) RunRegistration {
	return RunRegistration{Version: 1, RunID: intent.RunID, OperationID: intent.OperationID, IntentSHA256: birth.IntentSHA256, BirthSHA256: birthSHA, PID: birth.PID, UID: birth.UID, BootID: birth.BootID, StartTicks: birth.StartTicks, Generation: intent.Generation, ManifestSHA256: intent.ManifestSHA256, BinarySHA256: intent.Binary.SHA256, ConfigSHA256: intent.ConfigSHA256, NonceSHA256: intent.NonceSHA256, Controller: intent.Controller, Gate: "closed"}
}
func runStoreActiveUnchanged(t *testing.T, s *Store, before ActivePointer) {
	t.Helper()
	got, err := s.ReadActive()
	if err != nil || !reflect.DeepEqual(got, before) {
		t.Fatal("run records changed active:", err)
	}
}

func TestRunStorePrepareBindingAndTicket(t *testing.T) {
	p, s, intent, _ := runStoreFixture(t)
	for name, change := range map[string]func(*RunIntent){
		"manifest": func(r *RunIntent) { r.ManifestSHA256 = digest([]byte("wrong")) },
		"binary":   func(r *RunIntent) { r.Binary.Version = "different" },
		"config":   func(r *RunIntent) { r.ConfigSHA256 = digest([]byte("other-config")) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := intent
			change(&bad)
			before := tree(t, p.State)
			ticket, err := s.PrepareRun(bad)
			if err == nil || ticket != nil {
				t.Fatal("unbound intent accepted")
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("invalid preparation wrote record")
			}
		})
	}
	before, _ := s.ReadActive()
	var points []string
	s.fault = func(point string) error { points = append(points, point); return nil }
	ticket, err := s.PrepareRun(intent)
	s.fault = nil
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run.intent.write", "run.intent.sync", "run.intent.closed", "run.temp-sync", "run.rename", "run.parent-sync"}
	if !reflect.DeepEqual(points, want) {
		t.Fatal("intent persistence order:", points)
	}
	got, sha, err := s.ReadRunIntent(intent.RunID)
	if err != nil || !reflect.DeepEqual(got, intent) || sha != ticket.Digest() {
		t.Fatal("durable intent mismatch:", err)
	}
	if retry, err := s.PrepareRun(intent); retry != nil || !errors.Is(err, ErrRunConflict) {
		t.Fatal("same intent granted new spawn ticket:", err)
	}
	fd, err := ticket.TakeBirthDirectory()
	if err != nil {
		t.Fatal(err)
	}
	fd.Close()
	if fd, err := ticket.TakeBirthDirectory(); fd != nil || !errors.Is(err, ErrRunConflict) {
		t.Fatal("ticket reused:", err)
	}
	runStoreActiveUnchanged(t, s, before)
	intent.RunID = "closed-ticket"
	closedTicket, err := s.PrepareRun(intent)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if fd, err := closedTicket.TakeBirthDirectory(); fd != nil || !errors.Is(err, ErrClosed) {
		t.Fatal("closed writer transferred directory:", err)
	}
	if _, err := s.PrepareRun(intent); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	if _, _, err := s.ReadRunIntent(intent.RunID); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestRunStoreRegistrationAndReadOnly(t *testing.T) {
	p, s, intent, nonce := runStoreFixture(t)
	active, _ := s.ReadActive()
	birth, birthSHA, _ := runStoreBirth(t, s, intent, nonce)
	reg := runStoreRegistration(intent, birth, birthSHA)
	bad := reg
	bad.StartTicks++
	before := tree(t, p.State)
	if _, err := s.PublishRunRegistration(bad); !errors.Is(err, ErrIntegrity) {
		t.Fatal("cross-birth registration accepted:", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("bad registration mutated tree")
	}
	sha, err := s.PublishRunRegistration(reg)
	if err != nil {
		t.Fatal(err)
	}
	before = tree(t, p.State)
	if again, err := s.PublishRunRegistration(reg); err != nil || again != sha {
		t.Fatal("same registration not idempotent:", err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("idempotent registration changed bytes/mtime")
	}
	bad = reg
	bad.Gate = "open"
	if _, err := s.PublishRunRegistration(bad); !errors.Is(err, ErrRunConflict) {
		t.Fatal("registration overwritten:", err)
	}
	reg.OperationID = "recovery-operation"
	if _, err := s.PublishRunRegistration(reg); err != nil {
		t.Fatal("new recovery registration rejected:", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before = tree(t, p.State)
	reader, err := OpenStore(p, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.fault = func(point string) error {
		t.Errorf("reader invoked write/sync: %s", point)
		return errors.New("unexpected reader write")
	}
	if _, _, err := reader.ReadRunIntent(intent.RunID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reader.ReadRunBirth(intent.RunID); err != nil {
		t.Fatal(err)
	}
	for _, op := range []ID{intent.OperationID, reg.OperationID} {
		got, _, err := reader.ReadRunRegistration(intent.RunID, op)
		if err != nil || got.OperationID != op {
			t.Fatal("registration read:", err)
		}
	}
	if _, err := reader.PrepareRun(intent); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, err := reader.PublishRunRegistration(reg); !errors.Is(err, ErrReadOnly) {
		t.Fatal(err)
	}
	if _, _, err := reader.ReadRunIntent("missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, tree(t, p.State)) {
		t.Fatal("reader changed tree including mtime")
	}
	runStoreActiveUnchanged(t, reader, active)
}

func TestRunStoreMissingBirthAndUnsafeRecord(t *testing.T) {
	_, s, intent, _ := runStoreFixture(t)
	ticket, dir := runStorePrepare(t, s, intent)
	fake := RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: ticket.Digest(), PID: os.Getpid(), UID: uint32(os.Geteuid()), BootID: "00000000-0000-0000-0000-000000000000", StartTicks: 1}
	if _, err := s.PublishRunRegistration(runStoreRegistration(intent, fake, digest([]byte("missing")))); err == nil {
		t.Fatal("registration without birth accepted")
	}
	f, err := dir.OpenFile("intent.json", os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("{}\n"))
	f.Close()
	if _, _, err := s.ReadRunIntent(intent.RunID); !errors.Is(err, ErrIntegrity) {
		t.Fatal("corrupt intent accepted:", err)
	}
}

func TestRunStoreIntentFailureBoundaries(t *testing.T) {
	for _, point := range []string{"run.intent.write", "run.intent.sync", "run.intent.closed", "run.temp-sync", "run.rename", "run.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p, s, intent, _ := runStoreFixture(t)
			active, _ := s.ReadActive()
			injected := errors.New("intent boundary failure")
			hit := 0
			s.fault = func(at string) error {
				if at == point {
					hit++
					return injected
				}
				return nil
			}
			ticket, err := s.PrepareRun(intent)
			uncertain := point == "run.parent-sync"
			if ticket != nil || !errors.Is(err, injected) || hit != 1 || errors.Is(err, ErrUncertain) != uncertain {
				t.Fatalf("boundary result %v hits=%d", err, hit)
			}
			s.fault = nil
			runStoreActiveUnchanged(t, s, active)
			if uncertain {
				if _, _, err := s.ReadRunIntent(intent.RunID); err != nil {
					t.Fatal("published intent unreadable:", err)
				}
				if _, err := s.PrepareRun(intent); !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned writer reused:", err)
				}
				s.Close()
				s = openWriter(t, p)
				if _, _, err := s.ReadRunIntent(intent.RunID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PrepareRun(intent); !errors.Is(err, ErrRunConflict) {
					t.Fatal("reopen minted ticket:", err)
				}
			} else {
				if _, _, err := s.ReadRunIntent(intent.RunID); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("prepublication authority:", err)
				}
				if _, err := s.PrepareRun(intent); err != nil {
					t.Fatal("unpublished retry rejected:", err)
				}
			}
		})
	}
}

func TestRunStoreRegistrationFailureBoundaries(t *testing.T) {
	for _, point := range []string{"registration.file.write", "registration.file.sync", "registration.file.closed", "registration.rename", "registration.parent-sync"} {
		t.Run(point, func(t *testing.T) {
			p, s, intent, nonce := runStoreFixture(t)
			active, _ := s.ReadActive()
			birth, birthSHA, _ := runStoreBirth(t, s, intent, nonce)
			reg := runStoreRegistration(intent, birth, birthSHA)
			injected := errors.New("registration boundary failure")
			hit := 0
			s.fault = func(at string) error {
				if at == point {
					hit++
					return injected
				}
				return nil
			}
			sha, err := s.PublishRunRegistration(reg)
			uncertain := point == "registration.parent-sync"
			if sha != "" || !errors.Is(err, injected) || hit != 1 || errors.Is(err, ErrUncertain) != uncertain {
				t.Fatalf("boundary result %v hits=%d", err, hit)
			}
			s.fault = nil
			runStoreActiveUnchanged(t, s, active)
			if uncertain {
				if _, _, err := s.ReadRunRegistration(intent.RunID, reg.OperationID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.PublishRunRegistration(reg); !errors.Is(err, ErrUncertain) {
					t.Fatal("poisoned writer reused:", err)
				}
				s.Close()
				s = openWriter(t, p)
			} else if _, _, err := s.ReadRunRegistration(intent.RunID, reg.OperationID); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("prepublication registration:", err)
			}
			if _, err := s.PublishRunRegistration(reg); err != nil {
				t.Fatal("retry/reopen idempotence:", err)
			}
		})
	}
}

func TestRunStorePerRunSyncAndCapacity(t *testing.T) {
	t.Run("per-run-sync", func(t *testing.T) {
		p, s, intent, _ := runStoreFixture(t)
		for _, id := range []RunID{"first", "second"} {
			intent.RunID = id
			if _, err := s.PrepareRun(intent); err != nil {
				t.Fatal(err)
			}
		}
		calls := 0
		s.fault = func(point string) error {
			if point == "open.run.sync" {
				calls++
			}
			return nil
		}
		if err := s.syncRunDirectories(); err != nil || calls != 2 {
			t.Fatal("per-run barrier not traversed:", calls, err)
		}
		injected := errors.New("per-run sync failure")
		s.fault = func(point string) error {
			if point == "open.run.sync" {
				return injected
			}
			return nil
		}
		if err := s.syncRunDirectories(); !errors.Is(err, injected) {
			t.Fatal("sync error lost:", err)
		}
		s.fault = nil
		s.Close()
		// The explicit writer-open barrier also rejects an unsafe existing run dir.
		name, _ := runDirectory("first")
		if err := os.Chmod(filepath.Join(p.State, "runs", name), 0755); err != nil {
			t.Fatal(err)
		}
		unchanged := tree(t, p.State)
		reader, err := OpenStore(p, false)
		if err != nil {
			t.Fatal("reader unexpectedly traversed the per-run writer barrier:", err)
		}
		reader.Close()
		if !reflect.DeepEqual(unchanged, tree(t, p.State)) {
			t.Fatal("reader-open changed run tree")
		}
		if writer, err := OpenStore(p, true); err == nil {
			writer.Close()
			t.Fatal("writer skipped run validation barrier")
		}
		if err := os.Chmod(filepath.Join(p.State, "runs", name), 0700); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("run-capacity", func(t *testing.T) {
		p, s, intent, _ := runStoreFixture(t)
		// Capacity-only synthetic empty entries: no 1024 repeated fsync publications.
		for i := 0; i < MaxRuns; i++ {
			if err := os.Mkdir(filepath.Join(p.State, "runs", fmt.Sprintf("capacity-%04d", i)), 0700); err != nil {
				t.Fatal(err)
			}
		}
		if ticket, err := s.PrepareRun(intent); ticket != nil || !errors.Is(err, ErrRunConflict) {
			t.Fatal("run capacity ignored:", err)
		}
	})
	t.Run("registration-capacity", func(t *testing.T) {
		_, s, intent, nonce := runStoreFixture(t)
		birth, sha, dir := runStoreBirth(t, s, intent, nonce)
		for i := 0; i < MaxRunRegistrations; i++ {
			f, err := dir.OpenFile(fmt.Sprintf("registration-capacity-%04d.json", i), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				t.Fatal(err)
			}
			f.Close()
		}
		if _, err := s.PublishRunRegistration(runStoreRegistration(intent, birth, sha)); !errors.Is(err, ErrRunConflict) {
			t.Fatal("registration capacity ignored:", err)
		}
	})
}
