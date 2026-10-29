//go:build linux && amd64

package runtime

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hoopa/internal/config"
)

func prepareLaunchFixture(t *testing.T) (*ownershipFixture, config.RunIntent) {
	t.Helper()
	f, _ := ownershipPrepared(t, "127.0.0.1:23456", false)
	input := f.intent
	input.RunID = ""
	input.NonceSHA256 = ""
	input.Arguments = append([]string(nil), input.Arguments...)
	return f, input
}
func prepareLaunchHex(value string, size int) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == size && hex.EncodeToString(b) == value
}
func TestPrepareLaunchIndependentDurableInputs(t *testing.T) {
	f, input := prepareLaunchFixture(t)
	saved := input
	saved.Arguments = append([]string(nil), input.Arguments...)
	// The existing fixture only publishes storage: this test spawns no process.
	environment := append([]string(nil), os.Environ()...)
	first, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, saved) {
		t.Fatal("PrepareLaunch modified request")
	}
	if !reflect.DeepEqual(environment, os.Environ()) {
		t.Fatal("PrepareLaunch modified process environment")
	}
	var ids []config.RunID
	var nonces []string
	for _, prepared := range []*PreparedLaunch{first, second} {
		intent := prepared.Intent()
		if intent.Generation != input.Generation || intent.OperationID != input.OperationID {
			t.Fatal("caller identities changed")
		}
		if !strings.HasPrefix(string(intent.RunID), "run:") || !prepareLaunchHex(strings.TrimPrefix(string(intent.RunID), "run:"), 16) {
			t.Fatal("run ID shape")
		}
		original, sha, err := f.store.ReadRunIntent(intent.RunID)
		if err != nil || !reflect.DeepEqual(original, intent) {
			t.Fatal("intent not durable before transfer:", err)
		}
		directory, intentSHA, nonce, err := prepared.TakeLauncherInput()
		if err != nil {
			t.Fatal(err)
		}
		if directory == nil || intentSHA != sha || !prepareLaunchHex(nonce, 32) || ownershipDigest([]byte(nonce)) != intent.NonceSHA256 {
			t.Fatal("launcher inputs not bound")
		}
		root, err := os.OpenRoot(fmt.Sprintf("/proc/self/fd/%d", directory.Fd()))
		if err != nil {
			directory.Close()
			t.Fatal(err)
		}
		read, err := config.ReadLauncherIntent(root, intentSHA)
		root.Close()
		directory.Close()
		if err != nil || !reflect.DeepEqual(read, intent) {
			t.Fatal("transferred FD does not bind durable intent:", err)
		}
		if fd, sha, raw, err := prepared.TakeLauncherInput(); fd != nil || sha != "" || raw != "" || !errors.Is(err, config.ErrRunConflict) {
			if fd != nil {
				fd.Close()
			}
			t.Fatal("second transfer disclosed launch input")
		}
		ids = append(ids, intent.RunID)
		nonces = append(nonces, nonce)
	}
	if ids[0] == ids[1] || nonces[0] == nonces[1] {
		t.Fatal("independent preparations reused launch material")
	}
	// Both the incoming and outgoing slices must be detached from private state.
	before := first.Intent()
	input.Arguments[1] = "caller-mutation"
	exposed := first.Intent()
	exposed.Arguments[1] = "snapshot-mutation"
	if !reflect.DeepEqual(before, first.Intent()) {
		t.Fatal("caller modified prepared argv via alias")
	}
}

func TestPrepareLaunchRedactsRepresentations(t *testing.T) {
	f, input := prepareLaunchFixture(t)
	prepared, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	fd, _, nonce, err := prepared.TakeLauncherInput()
	if err != nil {
		t.Fatal(err)
	}
	fd.Close()
	for name, value := range map[string]any{"pointer": prepared, "value": *prepared, "nested-value": struct{ Prepared PreparedLaunch }{*prepared}, "nested-pointer": struct{ Prepared *PreparedLaunch }{prepared}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			text := fmt.Sprintf(format, value)
			if strings.Contains(text, nonce) {
				t.Fatalf("%s %s disclosed nonce", name, format)
			}
		}
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), nonce) {
			t.Fatal("JSON disclosed nonce")
		}
	}
	raw, err := json.Marshal(prepared)
	if err != nil || string(raw) != "{}" {
		t.Fatal("prepared object has JSON-exported state")
	}
	raw, err = json.Marshal(prepared.Intent())
	if err != nil || strings.Contains(string(raw), nonce) {
		t.Fatal("intent disclosed raw nonce")
	}
	if prepared.String() != "PreparedLaunch{redacted}" || prepared.GoString() != "PreparedLaunch{redacted}" {
		t.Fatal("representation not fixed")
	}
}

func TestPrepareLaunchRejectsWithoutNewRecords(t *testing.T) {
	f, input := prepareLaunchFixture(t)
	for name, change := range map[string]func(*config.RunIntent){
		"prefilled-run":   func(r *config.RunIntent) { r.RunID = "old-run" },
		"prefilled-nonce": func(r *config.RunIntent) { r.NonceSHA256 = ownershipDigest([]byte("old-nonce")) },
		"bad-shape":       func(r *config.RunIntent) { r.Arguments = nil },
		"bad-manifest":    func(r *config.RunIntent) { r.ManifestSHA256 = ownershipDigest([]byte("wrong")) },
		"bad-binary":      func(r *config.RunIntent) { r.Binary.Version = "wrong-binary" },
		"bad-config":      func(r *config.RunIntent) { r.ConfigSHA256 = ownershipDigest([]byte("wrong-config")) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := input
			change(&bad)
			before := ownershipTree(t, f.paths.State)
			if prepared, err := PrepareLaunch(f.store, bad); prepared != nil || err == nil {
				t.Fatal("invalid preparation accepted")
			}
			if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
				t.Fatal("rejected input created record")
			}
		})
	}
	if prepared, err := PrepareLaunch(nil, input); prepared != nil || err == nil {
		t.Fatal("nil store accepted")
	}
	f.store.Close()
	reader, err := config.OpenStore(f.paths, false)
	if err != nil {
		t.Fatal(err)
	}
	f.store = reader
	before := ownershipTree(t, f.paths.State)
	if prepared, err := PrepareLaunch(reader, input); prepared != nil || !errors.Is(err, config.ErrReadOnly) {
		t.Fatal("reader created launch ticket:", err)
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("read-only prepare mutated tree")
	}
}

func TestPrepareLaunchFailedTransferWithholdsNonce(t *testing.T) {
	f, input := prepareLaunchFixture(t)
	prepared, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	f.store.Close()
	fd, sha, nonce, err := prepared.TakeLauncherInput()
	if fd != nil || sha != "" || nonce != "" || !errors.Is(err, config.ErrClosed) {
		if fd != nil {
			fd.Close()
		}
		t.Fatal("failed directory transfer disclosed nonce:", err)
	}
	if _, _, nonce, err := prepared.TakeLauncherInput(); nonce != "" || !errors.Is(err, config.ErrRunConflict) {
		t.Fatal("failed transfer ticket reused")
	}
	for _, invalid := range []*PreparedLaunch{nil, {}} {
		if fd, sha, nonce, err := invalid.TakeLauncherInput(); fd != nil || sha != "" || nonce != "" || err == nil {
			t.Fatal("invalid prepared object disclosed input")
		}
	}
	if got := (*PreparedLaunch)(nil).Intent(); !reflect.DeepEqual(got, config.RunIntent{}) {
		t.Fatal("nil Intent not empty")
	}
}

func TestPrepareLaunchConcurrentTransferIsOnce(t *testing.T) {
	f, input := prepareLaunchFixture(t)
	prepared, err := PrepareLaunch(f.store, input)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		fd         *os.File
		sha, nonce string
		err        error
	}
	results := make(chan result, 2)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-gate
			fd, sha, nonce, err := prepared.TakeLauncherInput()
			results <- result{fd, sha, nonce, err}
		}()
	}
	close(gate)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for r := range results {
		if r.err == nil {
			success++
			if r.fd == nil || r.sha == "" || !prepareLaunchHex(r.nonce, 32) {
				t.Fatal("incomplete successful transfer")
			}
			r.fd.Close()
		} else {
			conflict++
			if r.fd != nil || r.sha != "" || r.nonce != "" || !errors.Is(r.err, config.ErrRunConflict) {
				t.Fatal("losing transfer disclosed input")
			}
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal("ticket not once-only:", success, conflict)
	}
}
