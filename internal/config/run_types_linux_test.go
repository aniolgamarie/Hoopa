//go:build linux

package config

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func runTestRecords() (RunIntent, RunBirth, RunRegistration) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	intent := RunIntent{Version: 1, RunID: "run-1", OperationID: "birth-op", Generation: "bundle-1", ManifestSHA256: a,
		Binary: BinaryReference{Path: "/opt/core", Version: "v1", SHA256: b}, Workdir: "/var/lib/hoopa/run-1", Arguments: []string{"/opt/core", "-d", "/var/lib/hoopa/run-1"}, Controller: "127.0.0.1:12345", ConfigSHA256: a, NonceSHA256: b}
	birth := RunBirth{Version: 1, RunID: intent.RunID, IntentSHA256: a, PID: 123, UID: 1002, BootID: "01234567-89ab-cdef-0123-456789abcdef", StartTicks: 12345678901234567890}
	reg := RunRegistration{Version: 1, RunID: intent.RunID, OperationID: "recovery-op", IntentSHA256: a, BirthSHA256: b, PID: birth.PID, UID: birth.UID, BootID: birth.BootID, StartTicks: birth.StartTicks, Generation: intent.Generation, ManifestSHA256: intent.ManifestSHA256, BinarySHA256: intent.Binary.SHA256, ConfigSHA256: intent.ConfigSHA256, NonceSHA256: intent.NonceSHA256, Controller: intent.Controller, Gate: "closed"}
	return intent, birth, reg
}
func runTestIntegrity(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("want integrity error, got %v", err)
	}
}

func TestRunValidRecordsAndRecoveryBinding(t *testing.T) {
	intent, birth, reg := runTestRecords()
	for _, gate := range []string{"closed", "open"} {
		t.Run(gate, func(t *testing.T) {
			reg.Gate = gate
			if reg.OperationID == intent.OperationID {
				t.Fatal("fixture must exercise new recovery operation")
			}
			if err := birth.Match(intent, birth.IntentSHA256); err != nil {
				t.Fatal(err)
			}
			if err := reg.Match(intent, birth.IntentSHA256, birth, reg.BirthSHA256); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Bounds are byte counts, not rune counts; empty later argv items are legal.
	intent.Arguments = []string{intent.Binary.Path, ""}
	intent.Binary.Version = strings.Repeat("v", 128)
	intent.Controller = "127.0.0.1:65535"
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	birth.PID = 2147483647
	birth.UID = math.MaxUint32
	birth.StartTicks = math.MaxUint64
	birth.BootID = "00000000-0000-0000-0000-000000000000" // no UUID version restriction
	if err := birth.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunIntentBoundsAndCanonicalAuthority(t *testing.T) {
	cases := map[string]func(*RunIntent){
		"wrong-version":         func(r *RunIntent) { r.Version = 2 },
		"invalid-token":         func(r *RunIntent) { r.Generation = "has space" },
		"uppercase-digest":      func(r *RunIntent) { r.NonceSHA256 = strings.Repeat("A", 64) },
		"relative-binary":       func(r *RunIntent) { r.Binary.Path = "core"; r.Arguments[0] = "core" },
		"unclean-workdir":       func(r *RunIntent) { r.Workdir = "/run/../work" },
		"nul-path":              func(r *RunIntent) { r.Workdir = "/run/\x00work" },
		"path-overflow":         func(r *RunIntent) { r.Workdir = "/" + strings.Repeat("x", 4096) },
		"empty-version":         func(r *RunIntent) { r.Binary.Version = "" },
		"version-overflow":      func(r *RunIntent) { r.Binary.Version = strings.Repeat("v", 129) },
		"argv-empty":            func(r *RunIntent) { r.Arguments = nil },
		"argv-wrong-executable": func(r *RunIntent) { r.Arguments[0] = "/other/core" },
		"argv-nul":              func(r *RunIntent) { r.Arguments = append(r.Arguments, "secret\x00") },
		"argv-item-overflow":    func(r *RunIntent) { r.Arguments = append(r.Arguments, strings.Repeat("é", 2049)) },
		"argv-count-overflow":   func(r *RunIntent) { r.Arguments = append([]string{r.Binary.Path}, make([]string, 64)...) },
		"argv-total-overflow": func(r *RunIntent) {
			r.Arguments = []string{r.Binary.Path}
			for i := 0; i < 8; i++ {
				r.Arguments = append(r.Arguments, strings.Repeat("x", 4096))
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { r, _, _ := runTestRecords(); mutate(&r); runTestIntegrity(t, r.Validate()) })
	}
	for _, controller := range []string{"localhost:12345", "[::1]:12345", "127.0.0.2:12345", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:0123", "127.0.0.1:+123", "127.0.0.1:123 "} {
		t.Run("controller-"+controller, func(t *testing.T) {
			r, _, reg := runTestRecords()
			r.Controller = controller
			reg.Controller = controller
			runTestIntegrity(t, r.Validate())
			runTestIntegrity(t, reg.Validate())
		})
	}
	t.Run("exact-argument-bounds", func(t *testing.T) {
		r, _, _ := runTestRecords()
		r.Arguments = []string{r.Binary.Path}
		for i := 0; i < 7; i++ {
			r.Arguments = append(r.Arguments, strings.Repeat("x", 4096))
		}
		r.Arguments = append(r.Arguments, strings.Repeat("x", 4096-len(r.Binary.Path)))
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		r.Arguments = append(r.Arguments, make([]string, 64-len(r.Arguments))...)
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		r.Workdir = "/" + strings.Repeat("w", 4095)
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunIdentityShape(t *testing.T) {
	for name, mutate := range map[string]func(*RunBirth){
		"pid-zero": func(r *RunBirth) { r.PID = 0 }, "pid-negative": func(r *RunBirth) { r.PID = -1 }, "pid-overflow": func(r *RunBirth) { r.PID = 2147483648 },
		"zero-ticks": func(r *RunBirth) { r.StartTicks = 0 }, "uppercase-boot": func(r *RunBirth) { r.BootID = strings.ToUpper(r.BootID) },
		"compact-boot": func(r *RunBirth) { r.BootID = strings.ReplaceAll(r.BootID, "-", "") }, "misplaced-boot-separator": func(r *RunBirth) { r.BootID = "0123456-789ab-cdef-0123-456789abcdef0" },
	} {
		t.Run(name, func(t *testing.T) {
			_, birth, reg := runTestRecords()
			mutate(&birth)
			runTestIntegrity(t, birth.Validate())
			reg.PID = birth.PID
			reg.StartTicks = birth.StartTicks
			reg.BootID = birth.BootID
			runTestIntegrity(t, reg.Validate())
		})
	}
	_, _, reg := runTestRecords()
	reg.Gate = "unknown"
	runTestIntegrity(t, reg.Validate())
}

func TestRunPreciseDecimalAndStrictDecode(t *testing.T) {
	_, birth, reg := runTestRecords()
	for name, record := range map[string]any{"birth": birth, "registration": reg} {
		t.Run(name, func(t *testing.T) {
			original, err := encode(record)
			if err != nil {
				t.Fatal(err)
			}
			marker := []byte(`"start_ticks":"12345678901234567890"`)
			if !bytes.Contains(original, marker) {
				t.Fatal("start ticks lost precision or string encoding")
			}
			decodeRecord := func(data []byte) error {
				if name == "birth" {
					var r RunBirth
					if err := decode(data, &r); err != nil {
						return err
					}
					return r.Validate()
				}
				var r RunRegistration
				if err := decode(data, &r); err != nil {
					return err
				}
				return r.Validate()
			}
			if err := decodeRecord(original); err != nil {
				t.Fatal(err)
			}
			for _, ticks := range []string{`"0"`, `"-1"`, `"18446744073709551616"`, `"01"`, `"+1"`, `"1e2"`, `"1.0"`, `1`, `null`} {
				t.Run(ticks, func(t *testing.T) {
					data := bytes.Replace(original, marker, []byte(`"start_ticks":`+ticks), 1)
					runTestIntegrity(t, decodeRecord(data))
				})
			}
			max := bytes.Replace(original, marker, []byte(`"start_ticks":"18446744073709551615"`), 1)
			if err := decodeRecord(max); err != nil {
				t.Fatal(err)
			}
			for label, data := range map[string][]byte{
				"unknown":      bytes.Replace(original, []byte(`{"version":1`), []byte(`{"unknown":1,"version":1`), 1),
				"duplicate":    bytes.Replace(original, []byte(`{"version":1`), []byte(`{"version":1,"version":1`), 1),
				"omitted":      bytes.Replace(original, []byte(`"uid":1002,`), nil, 1),
				"uid-overflow": bytes.Replace(original, []byte(`"uid":1002`), []byte(`"uid":4294967296`), 1),
			} {
				t.Run(label, func(t *testing.T) { runTestIntegrity(t, decodeRecord(data)) })
			}
		})
	}
}

func TestRunRegistrationRejectsCrossRecordBindings(t *testing.T) {
	// Each mutation stays shape-valid; Match must reject replacement authorities.
	for name, mutate := range map[string]func(*RunRegistration){
		"run": func(r *RunRegistration) { r.RunID = "other-run" }, "intent": func(r *RunRegistration) { r.IntentSHA256 = strings.Repeat("c", 64) }, "birth": func(r *RunRegistration) { r.BirthSHA256 = strings.Repeat("c", 64) },
		"pid": func(r *RunRegistration) { r.PID++ }, "uid": func(r *RunRegistration) { r.UID++ }, "boot": func(r *RunRegistration) { r.BootID = "ffffffff-ffff-ffff-ffff-ffffffffffff" }, "ticks": func(r *RunRegistration) { r.StartTicks++ },
		"generation": func(r *RunRegistration) { r.Generation = "other-bundle" }, "manifest": func(r *RunRegistration) { r.ManifestSHA256 = strings.Repeat("c", 64) }, "binary": func(r *RunRegistration) { r.BinarySHA256 = strings.Repeat("c", 64) }, "config": func(r *RunRegistration) { r.ConfigSHA256 = strings.Repeat("c", 64) }, "nonce": func(r *RunRegistration) { r.NonceSHA256 = strings.Repeat("c", 64) }, "controller": func(r *RunRegistration) { r.Controller = "127.0.0.1:1" },
	} {
		t.Run(name, func(t *testing.T) {
			intent, birth, reg := runTestRecords()
			mutate(&reg)
			if err := reg.Validate(); err != nil {
				t.Fatal("mutation must preserve shape:", err)
			}
			runTestIntegrity(t, reg.Match(intent, birth.IntentSHA256, birth, strings.Repeat("b", 64)))
		})
	}
}

func TestRunMatchValidatesOriginalsAndSuppliedDigests(t *testing.T) {
	for _, name := range []string{"bad-intent", "bad-birth", "bad-registration", "birth-other-run", "birth-other-intent", "supplied-intent", "supplied-birth", "malformed-intent-sha", "malformed-birth-sha"} {
		t.Run(name, func(t *testing.T) {
			intent, birth, reg := runTestRecords()
			intentSHA, birthSHA := birth.IntentSHA256, reg.BirthSHA256
			switch name {
			case "bad-intent":
				intent.Arguments = nil
			case "bad-birth":
				birth.StartTicks = 0
			case "bad-registration":
				reg.OperationID = ""
			case "birth-other-run":
				birth.RunID = "other-run"
			case "birth-other-intent":
				birth.IntentSHA256 = strings.Repeat("c", 64)
			case "supplied-intent":
				intentSHA = strings.Repeat("c", 64)
			case "supplied-birth":
				birthSHA = strings.Repeat("c", 64)
			case "malformed-intent-sha":
				intentSHA = "not-a-digest"
			case "malformed-birth-sha":
				birthSHA = "not-a-digest"
			}
			runTestIntegrity(t, reg.Match(intent, intentSHA, birth, birthSHA))
			if name != "bad-registration" && !strings.Contains(name, "birth-sha") && name != "supplied-birth" {
				runTestIntegrity(t, birth.Match(intent, intentSHA))
			}
		})
	}
}
