//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hoopa/internal/config"
)

func sourceWorkdirFixture(t *testing.T, binary string) (*Coordinator, config.Paths, sourceMaterialFixture, Request, config.ActivePointer) {
	t.Helper()
	if binary == "" {
		source := os.Getenv("HOOPA_P1_SOURCES_CORE")
		if source == "" {
			t.Skip("requires approved static sources core for actual ELF verification")
		}
		raw, err := os.ReadFile(source)
		if err != nil || hash(raw) != sourceStaticSHA {
			t.Fatal("approved static source core required", err)
		}
		// The checkout's /data ancestors are writable by other users. Keep the
		// exact approved bytes in a private single-link inode for positive proof.
		binary = filepath.Join(t.TempDir(), "mihomo")
		if err := os.WriteFile(binary, raw, 0700); err != nil {
			t.Fatal(err)
		}
	}
	c, p := coordinatorFixture(t, nil)
	initializePut(t, c, initializeSettings(t))
	_, root, err := c.OpenServiceRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	f := sourceMaterialBuild(t, binary)
	base, err := c.store.ReadActive()
	if err != nil {
		t.Fatal(err)
	}
	doc, err := planObject(f.plan.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	doc["base_revision"] = planRaw(t, base.Revision)
	delete(doc, "plan_digest") // Builder takes an unsigned envelope, unlike ParsePlan.
	f.plan, err = BuildPlan(planRaw(t, doc), sourceCoreValidator(f.compiled))
	if err != nil {
		t.Fatal(err)
	}
	f.material.Workdir = ""
	raw := planRaw(t, map[string]any{"api_version": "v1", "request_id": "source-workdir-fixture", "operation": "config.apply", "params": map[string]any{"plan": json.RawMessage(f.plan.Bytes()), "confirmed_plan_digest": f.plan.Digest(), "accept_disruption": true}})
	r, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return c, p, f, r, base
}

func TestSourceWorkdirPreparesPrivateFilesWithoutRuntimeAdmission(t *testing.T) {
	c, p, f, request, base := sourceWorkdirFixture(t, "")
	c.mu.Lock()
	checked, old, err := c.prepareSourceMaterialLocked(context.Background(), request, f.plan, base, f.material, nil)
	c.mu.Unlock()
	if err != nil || old != nil {
		t.Fatal("material preparation", err)
	}
	if filepath.Dir(checked.launch.Workdir) != p.Runtime || checked.launch.Workdir == "" || f.material.Workdir != "" {
		t.Fatal("workdir outside managed runtime or caller mutated")
	}
	probe := checked.launch
	probe.RunID = "workdir-test"
	probe.OperationID = request.OperationID()
	probe.NonceSHA256 = hash([]byte("shape-only"))
	directory, err := config.OpenRunWorkdir(probe)
	if err != nil {
		t.Fatal("ordinary full workdir proof failed", err)
	}
	directory.Close()
	active, _ := c.store.ReadActive()
	ids, _ := c.journal.IDs()
	runs, _ := c.store.ListRunIDs()
	if !reflect.DeepEqual(active, base) || len(ids) != 0 || len(runs) != 0 {
		t.Fatal("preparation created runtime authority")
	}
	assets, err := os.ReadDir(filepath.Join(p.State, "source-assets"))
	if err != nil || len(assets) != 1 {
		t.Fatal("durable name record", err)
	}
	raw, err := os.ReadFile(filepath.Join(p.State, "source-assets", assets[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Kind          string `json:"kind"`
		Workdir       string `json:"workdir_name"`
		PlanDigest    string `json:"plan_digest"`
		RequestDigest string `json:"request_digest"`
		ConfigSHA256  string `json:"config_sha256"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Kind != "source-apply-workdir" || record.Workdir != filepath.Base(checked.launch.Workdir) || record.PlanDigest != f.plan.Digest() || record.RequestDigest != request.Digest() || record.ConfigSHA256 != checked.launch.ConfigSHA256 {
		t.Fatal("preparation name not bound to exact request/plan/config")
	}
}

func TestSourceWorkdirRefusesBeforeAnyPreparationWrite(t *testing.T) {
	for _, kind := range []string{"supplied-workdir", "invalid-generated-bytes", "invalid-elf", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			binary := ""
			if kind == "invalid-elf" {
				binary = filepath.Join(t.TempDir(), "fake-core")
				if err := os.WriteFile(binary, []byte("not an ELF"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			c, p, f, r, base := sourceWorkdirFixture(t, binary)
			ctx := context.Background()
			switch kind {
			case "supplied-workdir":
				f.material.Workdir = f.root
			case "invalid-generated-bytes":
				for _, file := range f.material.Manifest.Files {
					if file.Kind == "generated_config" {
						f.material.Files[file.Name] = []byte("bad")
					}
				}
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before := coordinatorTree(t, p)
			c.mu.Lock()
			checked, old, err := c.prepareSourceMaterialLocked(ctx, r, f.plan, base, f.material, nil)
			c.mu.Unlock()
			if err == nil || old != nil || !reflect.DeepEqual(checked, checkedBasicMaterial{}) {
				t.Fatal("invalid preparation accepted")
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("rejected preparation wrote state")
			}
		})
	}
}

func TestSourceWorkdirInterruptedPreparationLeavesOnlyUnacceptedArtifacts(t *testing.T) {
	for _, cut := range []string{"apply-workdir-intent-published", "apply-workdir-created"} {
		t.Run(cut, func(t *testing.T) {
			c, p, f, r, base := sourceWorkdirFixture(t, "")
			stop := errors.New("synthetic preparation interruption")
			c.mu.Lock()
			checked, old, err := c.prepareSourceMaterialLocked(context.Background(), r, f.plan, base, f.material, func(point string) error {
				if point == cut {
					return stop
				}
				return nil
			})
			c.mu.Unlock()
			if !errors.Is(err, stop) || old != nil || !reflect.DeepEqual(checked, checkedBasicMaterial{}) {
				t.Fatal("interruption returned launch material", err)
			}
			ids, _ := c.journal.IDs()
			runs, _ := c.store.ListRunIDs()
			active, _ := c.store.ReadActive()
			if len(ids) != 0 || len(runs) != 0 || !reflect.DeepEqual(active, base) {
				t.Fatal("interrupted prep admitted operation/runtime")
			}
			entries, err := os.ReadDir(p.Runtime)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if cut == "apply-workdir-created" {
				want = 1
			}
			if len(entries) != want {
				t.Fatal("unexpected orphan workdir count", len(entries), want)
			}
			before := coordinatorTree(t, p)
			page, err := c.Sources(context.Background(), "")
			if err != nil || page.Total != 0 {
				t.Fatal("workdir artifact became source candidate", err)
			}
			if !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("query adopted or cleaned preparation")
			}
		})
	}
}
