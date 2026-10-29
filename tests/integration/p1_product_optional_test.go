//go:build linux && amd64 && p1integration

package integration

import (
	"context"
	"debug/buildinfo"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

var errP1OptionalEvidence = errors.New("P1_OPTIONAL_FIXTURE_SCOPE_UNVERIFIED")

// This finite graph check proves no extra Go driver modules in these builds.
// It is not a generic SQLite detector or audit of native/dynamic system calls.
func p1OptionalModuleGraph(info *debug.BuildInfo, mainPackage string) error {
	if (mainPackage != "hoopa/cmd/hoopa" && mainPackage != "hoopa/cmd/p1probe") || info == nil || info.Path != mainPackage || info.Main.Path != "hoopa" || info.Main.Replace != nil || len(info.Deps) != 2 {
		return errP1OptionalEvidence
	}
	allowed := map[string][2]string{
		"github.com/spf13/cobra": {"v1.10.2", "h1:DMTTonx5m65Ic0GOoRY2c16WCbHxOOw6xxezuLaBpcU="},
		"github.com/spf13/pflag": {"v1.0.9", "h1:9exaQaMOCwffKiiiYk6/BndUBv+iRViNW+4lEMi0PvY="},
	}
	for _, dep := range info.Deps {
		if dep == nil {
			return errP1OptionalEvidence
		}
		expected, ok := allowed[dep.Path]
		if !ok || dep.Replace != nil || dep.Version != expected[0] || dep.Sum != expected[1] {
			return errP1OptionalEvidence
		}
		delete(allowed, dep.Path)
	}
	if len(allowed) != 0 {
		return errP1OptionalEvidence
	}
	return nil
}

// Values are explicitly supplied fixture paths and LANG, never an ambient env
// dump. A PATH, when present, needs a separate anchored empty-directory proof.
func p1OptionalEnvironment(env []string, home string) (string, error) {
	expected := map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, "config"), "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_CACHE_HOME": filepath.Join(home, "cache"), "XDG_RUNTIME_DIR": filepath.Join(home, "runtime"), "TMPDIR": home, "LANG": "C"}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return "", errP1OptionalEvidence
	}
	seen := map[string]bool{}
	search := ""
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || seen[key] || strings.ContainsRune(value, 0) {
			return "", errP1OptionalEvidence
		}
		seen[key] = true
		if key == "PATH" {
			if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.Contains(value, ":") {
				return "", errP1OptionalEvidence
			}
			search = value
			continue
		}
		want, ok := expected[key]
		if !ok || want != value {
			return "", errP1OptionalEvidence
		}
	}
	for key := range expected {
		if !seen[key] {
			return "", errP1OptionalEvidence
		}
	}
	return search, nil
}

type p1OptionalBinary struct {
	Role          string           `json:"role"`
	Path          string           `json:"path"`
	SHA256        string           `json:"sha256"`
	Bytes         int64            `json:"bytes"`
	BuildInfo     *debug.BuildInfo `json:"build_info,omitempty"`
	BuildInfoText string           `json:"build_info_text,omitempty"`
}

func (h *p1ProductHarness) optionalComponentEvidence(ctx context.Context) (observed[[]string], error) {
	fail := func() (observed[[]string], error) {
		if ctx != nil && ctx.Err() != nil {
			return p1Missing[[]string]("optional_fixture_scope_unverified"), errors.Join(errP1OptionalEvidence, ctx.Err())
		}
		return p1Missing[[]string]("optional_fixture_scope_unverified"), errP1OptionalEvidence
	}
	if ctx == nil || ctx.Err() != nil || h == nil || h.f == nil || h.f.cli == nil {
		return fail()
	}
	env := h.f.cli.environment()
	search, err := p1OptionalEnvironment(env, h.f.cli.home)
	if err != nil {
		return fail()
	}
	pathKind := "PATH-absent"
	if search != "" {
		dir, err := p1OpenObserveCapture(search)
		if err != nil {
			return fail()
		}
		names, readErr := dir.Readdirnames(1)
		closeErr := dir.Close()
		if len(names) != 0 || readErr != io.EOF || closeErr != nil {
			return fail()
		}
		pathKind = "private-anchored-empty-PATH-directory"
	}
	if ctx.Err() != nil {
		return fail()
	}
	envRef, err := h.original("optional-child-environment", map[string]any{"kind": "explicit-fixture-child-environment", "environment": env, "path_evidence": pathKind, "path_directory": search, "scope": "no GOST executable resolution through the configured child search path; not host-wide absence"})
	if err != nil {
		return fail()
	}
	h.extras = append(h.extras, envRef)
	refs := []originalRef{envRef}
	inventory := []p1OptionalBinary{}
	for _, binary := range []struct{ role, path, main string }{{"cli", h.f.cli.executable, "hoopa/cmd/hoopa"}, {"probe", h.f.probe, "hoopa/cmd/p1probe"}} {
		if ctx.Err() != nil {
			return fail()
		}
		before, sha, err := p1CLIExecutable(binary.path)
		if err != nil {
			return fail()
		}
		if binary.role == "cli" && (!os.SameFile(before, h.f.cli.executableInfo) || sha != h.f.cli.executableSHA) {
			return fail()
		}
		info, err := buildinfo.ReadFile(binary.path)
		if err != nil || p1OptionalModuleGraph(info, binary.main) != nil {
			return fail()
		}
		after, again, err := p1CLIExecutable(binary.path)
		if err != nil || again != sha || !os.SameFile(before, after) || ctx.Err() != nil {
			return fail()
		}
		item := p1OptionalBinary{Role: binary.role, Path: binary.path, SHA256: sha, Bytes: before.Size(), BuildInfo: info, BuildInfoText: info.String()}
		ref, err := h.original("optional-build-info", map[string]any{"kind": "actual-fixed-executable-build-info-read", "binary": item, "scope": "finite linked Go-module graph, not native or dynamic library audit"})
		if err != nil {
			return fail()
		}
		h.extras = append(h.extras, ref)
		refs = append(refs, ref)
		inventory = append(inventory, item)
	}
	// The approved core is an explicit absolute executable too, never a PATH
	// dependency; include its real file pin in the finite executable inventory.
	info, coreSHA, err := p1CLIExecutable(h.f.core)
	if err != nil || coreSHA != "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb" || ctx.Err() != nil {
		return fail()
	}
	inventory = append(inventory, p1OptionalBinary{Role: "approved-basic-core", Path: h.f.core, SHA256: coreSHA, Bytes: info.Size()})
	receipt, err := h.original("optional-fixture-scope", map[string]any{"kind": "fixed-build-module-graph-and-child-launch-configuration", "executable_inventory": inventory, "environment_original": envRef, "components": []string{"gost", "sqlite"}, "gost_scope": "no configured child executable search path to GOST; all fixture executable paths explicit", "sqlite_scope": "Hoopa CLI/probe Go module graphs contain only pinned Cobra/pflag and no additional database-driver module", "limitations": []string{"not host-wide executable or SQLite absence", "not a dynamic syscall/native-library audit", "not derived from requested unavailable component labels"}})
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	h.extras = append(h.extras, receipt)
	// Each label is generated only after its independent environment/build check.
	refs = append(refs, receipt)
	return p1Fact([]string{"gost", "sqlite"}, refs...), nil
}

func p1OptionalSyntheticInfo() *debug.BuildInfo {
	return &debug.BuildInfo{Path: "hoopa/cmd/hoopa", Main: debug.Module{Path: "hoopa"}, Deps: []*debug.Module{{Path: "github.com/spf13/cobra", Version: "v1.10.2", Sum: "h1:DMTTonx5m65Ic0GOoRY2c16WCbHxOOw6xxezuLaBpcU="}, {Path: "github.com/spf13/pflag", Version: "v1.0.9", Sum: "h1:9exaQaMOCwffKiiiYk6/BndUBv+iRViNW+4lEMi0PvY="}}}
}

// Synthetic metadata tests only, not evidence about an actual executable.
func TestP1OptionalModuleGraph(t *testing.T) {
	for _, main := range []string{"hoopa/cmd/hoopa", "hoopa/cmd/p1probe"} {
		info := p1OptionalSyntheticInfo()
		info.Path = main
		must(t, p1OptionalModuleGraph(info, main))
	}
	for _, change := range []string{"nil", "main-package", "main-module", "main-replace", "extra", "missing", "duplicate", "nil-dep", "replace", "version", "sum", "unknown"} {
		t.Run(change, func(t *testing.T) {
			info := p1OptionalSyntheticInfo()
			switch change {
			case "nil":
				info = nil
			case "main-package":
				info.Path = "other"
			case "main-module":
				info.Main.Path = "other"
			case "main-replace":
				info.Main.Replace = &debug.Module{Path: "other"}
			case "extra":
				info.Deps = append(info.Deps, &debug.Module{Path: "sqlite-driver"})
			case "missing":
				info.Deps = info.Deps[:1]
			case "duplicate":
				info.Deps[1] = info.Deps[0]
			case "nil-dep":
				info.Deps[1] = nil
			case "replace":
				info.Deps[0].Replace = &debug.Module{Path: "other"}
			case "version":
				info.Deps[0].Version = "other"
			case "sum":
				info.Deps[0].Sum = "other"
			case "unknown":
				info.Deps[0].Path = "other"
			}
			if p1OptionalModuleGraph(info, "hoopa/cmd/hoopa") == nil {
				t.Fatal("unknown synthetic module graph accepted")
			}
		})
	}
}
func TestP1OptionalEnvironment(t *testing.T) {
	cli := &p1CLITransport{home: "/synthetic/private-home"}
	env := cli.environment()
	search, err := p1OptionalEnvironment(env, cli.home)
	must(t, err)
	if search != "" {
		t.Fatal("invented PATH")
	}
	for _, change := range []string{"proxy", "credential", "driver", "duplicate", "missing", "wrong-home", "path-empty", "path-dot", "path-list", "path-relative"} {
		t.Run(change, func(t *testing.T) {
			copyEnv := append([]string(nil), env...)
			switch change {
			case "proxy":
				copyEnv = append(copyEnv, "HTTPS_PROXY=synthetic-private-value")
			case "credential":
				copyEnv = append(copyEnv, "TOKEN=synthetic-private-value")
			case "driver":
				copyEnv = append(copyEnv, "HOOPA_P1_CLI_TRANSPORT_DRIVER=fake")
			case "duplicate":
				copyEnv = append(copyEnv, env[0])
			case "missing":
				copyEnv = copyEnv[1:]
			case "wrong-home":
				copyEnv[0] = "HOME=/other"
			case "path-empty":
				copyEnv = append(copyEnv, "PATH=")
			case "path-dot":
				copyEnv = append(copyEnv, "PATH=.")
			case "path-list":
				copyEnv = append(copyEnv, "PATH=/private:/usr/bin")
			case "path-relative":
				copyEnv = append(copyEnv, "PATH=relative")
			}
			if _, err := p1OptionalEnvironment(copyEnv, cli.home); err == nil {
				t.Fatal("unverified child environment accepted")
			}
		})
	}
	// Shape alone is not emptiness/ownership proof; the actual helper additionally
	// opens this path without symlinks and requires private0700 plus EOF listing.
	search, err = p1OptionalEnvironment(append(append([]string(nil), env...), "PATH=/synthetic/empty"), cli.home)
	must(t, err)
	if search != "/synthetic/empty" {
		t.Fatal("explicit PATH shape lost")
	}
}

func TestP1OptionalEvidenceUnknown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var h *p1ProductHarness
	result, err := h.optionalComponentEvidence(ctx)
	if !errors.Is(err, context.Canceled) || result.State != "unknown" || result.Value != nil {
		t.Fatal("cancellation fabricated component evidence")
	}
	result, err = h.optionalComponentEvidence(context.Background())
	if !errors.Is(err, errP1OptionalEvidence) || result.State != "unknown" || result.Value != nil {
		t.Fatal("missing fixture fabricated component evidence")
	}
}
