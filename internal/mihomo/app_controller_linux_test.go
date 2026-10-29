//go:build linux && amd64

package mihomo

import (
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
	sources "hoopa/protocol/sourcecontrol"
)

func appRoutingCaps() []string {
	return []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "ordered-app-rules-v1", "probe-qualified-groups-v1", "unknown-ip-reject-v1"}
}

// Synthetic declaration tests expected-profile binding, not executable approval
// or the runtime rule/unknown-IP behavior of a real app core.
func appRoutingBlueprint(t *testing.T, address string) (inspectorBlueprint, sources.Snapshot) {
	t.Helper()
	b, s := sourceBlueprint(t, address)
	b.record.Mode = "basic-app-routing"
	b.record.Capabilities = appRoutingCaps()
	b.record.EvidenceSHA256 = sha256String([]byte("synthetic app controller profile binding; no binary approval"))
	var e error
	b.capabilityDigest, e = b.record.Digest()
	if e != nil {
		t.Fatal(e)
	}
	return b, s
}
func appRoutingStatus(t *testing.T, b inspectorBlueprint, s sources.Snapshot, state string) wire.Status {
	t.Helper()
	sourceB := b
	sourceB.record.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"}
	v := sourceStatus(t, sourceB, s, state)
	v.Mode = b.record.Mode
	v.Capabilities = append([]string{}, b.record.Capabilities...)
	if _, e := sources.DecodeStatus(inspectorJSON(t, v)); e != nil {
		t.Fatal("invalid expected-profile fixture", e)
	}
	return v
}
func TestAppRoutingStatusProjectionPreservesV2Profile(t *testing.T) {
	b, s := appRoutingBlueprint(t, "127.0.0.1:32003")
	for _, state := range []string{"closed", "open", "draining", "degraded"} {
		t.Run(state, func(t *testing.T) {
			expected := appRoutingStatus(t, b, s, state)
			raw := inspectorJSON(t, expected)
			got, e := decodeControllerStatus(2, raw)
			if e != nil || !reflect.DeepEqual(got, expected) || got.Mode != "basic-app-routing" || got.SchemaVersion != 2 {
				t.Fatal("v2 adapter changed qualified profile/history", e)
			}
			if got.Validate() == nil {
				t.Fatal("v2 carrier became v1 authority")
			}
			if _, e = decodeControllerStatus(1, raw); e == nil {
				t.Fatal("v1 downgrade accepted app status")
			}
			got.Capabilities[0] = "changed"
			for i := range raw {
				raw[i] = 'x'
			}
			if expected.Capabilities[0] != "authenticated-activate-v1" {
				t.Fatal("carrier leaked mutable capability alias")
			}
		})
	}
}

func TestAppRoutingControllerProfileBindingsEveryStatusRead(t *testing.T) {
	for _, termination := range []bool{false, true} {
		for _, expectedApp := range []bool{false, true} {
			for _, when := range []string{"valid", "first-other-profile", "last-other-profile", "both-other-profile", "first-mode-only", "last-mode-only", "first-caps-only", "last-caps-only", "first-schema1", "last-schema1"} {
				name := when + "/expected-source"
				if expectedApp {
					name = when + "/expected-app"
				}
				if termination {
					name = "termination/" + name
				} else {
					name = "inspect/" + name
				}
				t.Run(name, func(t *testing.T) {
					ln, e := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
					if e != nil {
						t.Fatal(e)
					}
					defer ln.Close()
					var b inspectorBlueprint
					var snapshot sources.Snapshot
					if expectedApp {
						b, snapshot = appRoutingBlueprint(t, ln.Addr().String())
					} else {
						b, snapshot = sourceBlueprint(t, ln.Addr().String())
					}
					f := inspectorPublish(t, b)
					b = f.blueprint
					status := appRoutingStatus(t, b, snapshot, "closed")
					steps := sourceReadSteps(t, snapshot, status)
					wantCount := 3
					if when != "valid" {
						bad := status
						bad.Capabilities = append([]string{}, status.Capabilities...)
						otherMode := "basic-app-routing"
						otherCaps := appRoutingCaps()
						if expectedApp {
							otherMode = "basic-sources"
							otherCaps = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"}
						}
						if strings.Contains(when, "other-profile") {
							bad.Mode, bad.Capabilities = otherMode, otherCaps
							if _, e := sources.DecodeStatus(inspectorJSON(t, bad)); e != nil {
								t.Fatal("cross-profile negative must remain valid wire shape", e)
							}
						}
						if strings.Contains(when, "mode-only") {
							bad.Mode = otherMode
						}
						if strings.Contains(when, "caps-only") {
							bad.Capabilities = otherCaps
						}
						if strings.Contains(when, "schema1") {
							bad.SchemaVersion = 1
							bad.Mode = "basic"
							bad.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"}
						}
						index := 0
						if strings.HasPrefix(when, "last-") {
							index = 2
						} else {
							wantCount = 1
						}
						steps[index].Response = responseBytes(inspectorJSON(t, bad))
						if when == "both-other-profile" {
							steps[0].Response = responseBytes(inspectorJSON(t, bad))
							steps[2].Response = responseBytes(inspectorJSON(t, bad))
							wantCount = 1
						}
					}
					var got owned.ControllerObservation
					tree := inspectorTree(t, f.paths.State)
					count, err := terminationExchange(t, ln, b, steps, func(conn *net.TCPConn) error {
						var err error
						if termination {
							reg := mutationRegistration(b)
							reg.OperationID = b.intent.OperationID
							got, err = f.controller.InspectTermination(inspectorContext(t), conn, b.intent, reg, strings.Repeat("3", 64))
						} else {
							got, err = f.controller.Inspect(inspectorContext(t), conn, b.intent)
						}
						return err
					})
					if when == "valid" {
						if err != nil || count != 3 || got.RunID != b.intent.RunID || got.Gate != "closed" {
							t.Fatal("valid expected profile refused", count, err)
						}
					} else if !errors.Is(err, ErrController) || !reflect.DeepEqual(got, owned.ControllerObservation{}) || count != wantCount {
						t.Fatalf("foreign profile claim accepted or wrong read count: got=%v count=%d want=%d err=%v", got, count, wantCount, err)
					}
					if !reflect.DeepEqual(tree, inspectorTree(t, f.paths.State)) {
						t.Fatal("read-only profile check changed persistent state")
					}
				})
			}
		}
	}
}

func TestAppRoutingCapabilityShapeCannotGrantBinaryApproval(t *testing.T) {
	b, _ := appRoutingBlueprint(t, "127.0.0.1:32003")
	if b.record.Validate() != nil {
		t.Fatal("exact app shape rejected")
	}
	if _, e := ApprovedCapability(b.record.Binary); e == nil {
		t.Fatal("synthetic shape minted approval")
	}
	for name, edit := range map[string]func(*CapabilityRecord){"protocol": func(c *CapabilityRecord) { c.Protocol = 1 }, "mode": func(c *CapabilityRecord) { c.Mode = "basic-sources" }, "reorder": func(c *CapabilityRecord) { c.Capabilities[0], c.Capabilities[1] = c.Capabilities[1], c.Capabilities[0] }, "drop-unknown": func(c *CapabilityRecord) { c.Capabilities = c.Capabilities[:len(c.Capabilities)-1] }, "extra": func(c *CapabilityRecord) { c.Capabilities = append(c.Capabilities, "original-process-identity") }, "duplicate": func(c *CapabilityRecord) { c.Capabilities[5] = c.Capabilities[4] }, "not-accepted": func(c *CapabilityRecord) { c.Accepted = false }} {
		t.Run(name, func(t *testing.T) {
			c := b.record
			c.Capabilities = append([]string{}, c.Capabilities...)
			edit(&c)
			if c.Validate() != ErrCapability {
				t.Fatal("inexact app shape accepted")
			}
		})
	}
	old, e := ApprovedSourcesCapability("/synthetic/source-core")
	if e != nil {
		t.Fatal(e)
	}
	old.Mode = "basic-app-routing"
	old.Capabilities = appRoutingCaps()
	if old.Validate() != nil {
		t.Fatal("shape must remain distinct from evidence approval")
	}
	selected, e := ApprovedCapability(config.BinaryReference{Path: old.Binary.Path, Version: old.Binary.Version, SHA256: old.Binary.SHA256})
	if e != nil || selected.Mode != "basic-sources" {
		t.Fatal("old source binary promoted from caller's profile claim")
	}
}
