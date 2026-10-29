//go:build linux && amd64

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/control"
)

func cliPaths(t *testing.T) config.Paths {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "hoopa-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	for _, key := range []string{"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(key, filepath.Join(home, strings.ToLower(key)))
	}
	paths, err := resolvePaths()
	if err != nil {
		t.Fatal(err)
	}
	return paths
}
func cliTree(t *testing.T, p config.Paths) map[string]string {
	t.Helper()
	tree := map[string]string{}
	for _, root := range []string{p.Config, p.State, p.Cache, p.Runtime} {
		if _, err := os.Lstat(root); os.IsNotExist(err) {
			tree[root] = "absent"
			continue
		}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
			if info.Mode().IsRegular() {
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				value += ":" + string(raw)
			}
			tree[path] = value
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return tree
}
func cliResponse(t *testing.T, args []string, input io.Reader) (int, map[string]json.RawMessage, string) {
	t.Helper()
	var out, diagnostics bytes.Buffer
	code := Execute(context.Background(), args, input, &out, &diagnostics)
	decoder := json.NewDecoder(bytes.NewReader(out.Bytes()))
	var response map[string]json.RawMessage
	if err := decoder.Decode(&response); err != nil {
		t.Fatal("stdout is not JSON", code, err, out.String())
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("stdout had more than one JSON response", err, out.String())
	}
	if string(response["api_version"]) != `"v1"` || len(response) != 6 {
		t.Fatal("unexpected response envelope", out.String())
	}
	if diagnostics.Len() != 0 {
		t.Fatal("request mode wrote diagnostics", diagnostics.String())
	}
	return code, response, out.String()
}
func cliRequest(id, operation, params string) string {
	return fmt.Sprintf(`{"api_version":"v1","request_id":%q,"operation":%q,"params":%s}`, id, operation, params)
}
func TestCLIArgumentValidationDoesNotCreateStore(t *testing.T) {
	for _, args := range [][]string{{}, {"unknown-command"}, {"start", "--unknown"}, {"start", "extra"}, {"stop"}, {"stop", "--accept-disruption", "extra"}, {"service", "run", "--restart-once", "extra"}, {"operation", "get"}, {"operation", "get", "../invalid"}, {"operation", "recover", "../invalid"}, {"config", "apply"}, {"usage"}, {"api"}, {"api", "v1"}, {"api", "v1", "status.get", "--help"}, {"api", "v1", "--help"}, {"api", "v2", "status.get"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			p := cliPaths(t)
			before := cliTree(t, p)
			code, _, _ := cliResponse(t, args, strings.NewReader(""))
			if code != 2 {
				t.Fatal("invalid argv exit", code)
			}
			if !reflect.DeepEqual(before, cliTree(t, p)) {
				t.Fatal("argument failure created state")
			}
		})
	}
}
func TestCLIMachineIdentityBoundsAndBinding(t *testing.T) {
	for _, kind := range []string{"operation-mismatch", "version", "duplicate", "trailing", "oversize", "invalid-unicode", "start-extra", "start-null", "start-missing", "start-bad-revision"} {
		t.Run(kind, func(t *testing.T) {
			p := cliPaths(t)
			before := cliTree(t, p)
			args := []string{"api", "v1", "status.get"}
			raw := cliRequest("request-one", "status.get", `{}`)
			switch kind {
			case "operation-mismatch":
				raw = cliRequest("request-one", "service.stop", `{"expected_revision":"secret-body-token"}`)
			case "version":
				raw = strings.Replace(raw, `"v1"`, `"v2"`, 1)
			case "duplicate":
				raw = `{"api_version":"v1","request_id":"one","request_id":"two","operation":"status.get","params":{}}`
			case "trailing":
				raw += ` {"secret":"secret-body-token"}`
			case "oversize":
				raw = strings.Repeat(" ", control.MaxRequestBytes+1) + "secret-body-token"
			case "invalid-unicode":
				raw = strings.Replace(raw, `"request-one"`, `"\ud800"`, 1)
			case "start-extra":
				args[2] = "runtime.start"
				raw = cliRequest("request-one", "runtime.start", `{"expected_revision":"r1","control_only":true,"extra":"secret-body-token"}`)
			case "start-null":
				args[2] = "runtime.start"
				raw = cliRequest("request-one", "runtime.start", `{"expected_revision":"r1","control_only":null}`)
			case "start-missing":
				args[2] = "runtime.start"
				raw = cliRequest("request-one", "runtime.start", `{"expected_revision":"r1"}`)
			case "start-bad-revision":
				args[2] = "runtime.start"
				raw = cliRequest("request-one", "runtime.start", `{"expected_revision":"../bad","control_only":true}`)
			}
			code, _, out := cliResponse(t, args, strings.NewReader(raw))
			if code != 2 {
				t.Fatal("invalid machine input exit", code, out)
			}
			if strings.Contains(out, "secret-body-token") || !reflect.DeepEqual(before, cliTree(t, p)) {
				t.Fatal("input leaked or created state")
			}
		})
	}
}

type cliCountReader struct{ count int }

func (r *cliCountReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	r.count += len(p)
	return len(p), nil
}
func TestCLIMachineStdinRawLimit(t *testing.T) {
	p := cliPaths(t)
	before := cliTree(t, p)
	input := &cliCountReader{}
	code, _, out := cliResponse(t, []string{"api", "v1", "status.get"}, input)
	if code != 2 || input.count != control.MaxRequestBytes+1 || len(out) > control.MaxResultBytes || !reflect.DeepEqual(before, cliTree(t, p)) {
		t.Fatal(code, input.count, len(out))
	}
}
func TestCLIQueriesAreReadOnlyAndKeepRequestIdentity(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			p := cliPaths(t)
			if existing {
				store, err := config.OpenStore(p, true)
				if err != nil {
					t.Fatal(err)
				}
				if err = store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before := cliTree(t, p)
			for _, test := range []struct {
				args []string
				raw  string
				id   string
			}{{[]string{"status"}, "", ""}, {[]string{"api", "v1", "status.get"}, cliRequest("preserved-machine-id", "status.get", `{}`), "preserved-machine-id"}, {[]string{"operation", "get", "op-missing"}, "", ""}, {[]string{"api", "v1", "operation.get"}, cliRequest("operation-id", "operation.get", `{"operation_id":"op-missing"}`), "operation-id"}} {
				_, response, _ := cliResponse(t, test.args, strings.NewReader(test.raw))
				if test.id != "" && string(response["request_id"]) != fmt.Sprintf("%q", test.id) {
					t.Fatal("request identity lost", string(response["request_id"]))
				}
				if !reflect.DeepEqual(before, cliTree(t, p)) {
					t.Fatal("query mutated tree", test.args)
				}
			}
		})
	}
}
func TestCLIOrdinaryStartRequiresSnapshotWithoutCreation(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing"
		if existing {
			name = "initialized-empty"
		}
		t.Run(name, func(t *testing.T) {
			p := cliPaths(t)
			if existing {
				store, err := config.OpenStore(p, true)
				if err != nil {
					t.Fatal(err)
				}
				store.Close()
			}
			before := cliTree(t, p)
			code, _, _ := cliResponse(t, []string{"start"}, strings.NewReader(""))
			if code == 0 || !reflect.DeepEqual(before, cliTree(t, p)) {
				t.Fatal("empty ordinary start initialized or launched", code)
			}
		})
	}
}
func TestCLIMachineStartCannotCreateUnknownRevision(t *testing.T) {
	p := cliPaths(t)
	before := cliTree(t, p)
	for _, controlOnly := range []bool{false, true} {
		raw := cliRequest("api-no-store", "runtime.start", fmt.Sprintf(`{"expected_revision":"caller-unknown-revision","control_only":%v}`, controlOnly))
		code, _, _ := cliResponse(t, []string{"api", "v1", "runtime.start"}, strings.NewReader(raw))
		if code == 0 || !reflect.DeepEqual(before, cliTree(t, p)) {
			t.Fatal("machine request created unknown empty state", code)
		}
	}
}
func TestCLIInternalDispatchRejectsInvalidProtocolBeforePaths(t *testing.T) {
	p := cliPaths(t)
	before := cliTree(t, p)
	t.Setenv("HOOPA_LAUNCH_PROTOCOL", "")
	t.Setenv("HOOPA_SERVICE_PROTOCOL", "")
	for _, entry := range []string{coreLauncherEntry, serviceEntry} {
		var output, diagnostics bytes.Buffer
		code := Execute(context.Background(), []string{entry}, strings.NewReader(""), &output, &diagnostics)
		if code != 2 || output.Len() != 0 || diagnostics.String() != "hoopa: internal operation unavailable\n" || !reflect.DeepEqual(before, cliTree(t, p)) {
			t.Fatal("invalid internal entry had effects", entry, code, output.String(), diagnostics.String())
		}
	}
}
