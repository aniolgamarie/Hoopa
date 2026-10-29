package main

import (
	"fmt"
	"testing"
	"time"
)

func identityFixture() ([]ExpectedIdentityFlow, []IdentityObservation) {
	expected := make([]ExpectedIdentityFlow, 100)
	observed := make([]IdentityObservation, 100)
	for i := range expected {
		pid := 101 + i%2
		start := fmt.Sprintf("start-%d", pid)
		expected[i] = ExpectedIdentityFlow{ID: fmt.Sprintf("short-%03d", i+1), PID: pid, Name: "same-agent", StartIdentity: start, BootIdentity: "synthetic-boot", Domain: fmt.Sprintf("domain-%d.test", i%2), InputBytes: 3, OutputBytes: 5}
		observed[i] = IdentityObservation{FlowID: expected[i].ID, PID: pid, ProxyPID: 999, Name: "same-agent", StartIdentity: start, BootIdentity: "synthetic-boot", Domain: expected[i].Domain, SocketEvidenceID: "synthetic-original-socket", TargetEvidenceID: "synthetic-proxy-target", OriginalIngress: true, UniqueOwner: true, SourceObservedAt: time.Unix(1, 0), ForwardStartedAt: time.Unix(2, 0), Terminal: true, InputBytes: 3, OutputBytes: 5}
	}
	return expected, observed
}

func TestIdentityOracleRequiresEveryPreregisteredTerminalFlow(t *testing.T) {
	expected, observed := identityFixture()
	if result := CheckIdentityRun(expected, observed, true); result.Status != Passed {
		t.Fatalf("synthetic complete oracle: %+v", result)
	}
	for _, observations := range [][]IdentityObservation{nil, observed[:99]} {
		if result := CheckIdentityRun(expected, observations, true); result.Status != Unverified {
			t.Fatalf("missing observations claimed pass: %+v", result)
		}
	}
	if result := CheckIdentityRun(expected, observed, false); result.Status != Unverified {
		t.Fatal("unregistered runtime identities passed")
	}
	observed[99].Terminal = false
	if result := CheckIdentityRun(expected, observed, true); result.Status != Unverified {
		t.Fatal("short terminal fact omitted")
	}
}

func TestIdentityRejectsProxyPIDReuseLateEvidenceAndWrongTarget(t *testing.T) {
	for _, change := range []func(*IdentityObservation){
		func(o *IdentityObservation) { o.PID = o.ProxyPID },
		func(o *IdentityObservation) { o.StartIdentity = "reused-pid-new-start" },
		func(o *IdentityObservation) { o.Name = "parent-launcher" },
		func(o *IdentityObservation) { o.Domain = "guessed-from-shared-ip.test" },
		func(o *IdentityObservation) { o.InputBytes++ },
	} {
		expected, observed := identityFixture()
		change(&observed[0])
		if r := CheckIdentityRun(expected, observed, true); r.Status != Failed {
			t.Fatalf("incorrect identity/fact passed: %+v", r)
		}
	}
	expected, observed := identityFixture()
	observed[0].SourceObservedAt = time.Unix(3, 0)
	if r := CheckIdentityRun(expected, observed, true); r.Status != Unverified {
		t.Fatal("late identity claimed synchronous ingress evidence")
	}
	observed[0] = observed[1]
	if r := CheckIdentityRun(expected, observed, true); r.Status != Failed {
		t.Fatal("duplicate observations accepted")
	}
}

func TestIdentitySharedSocketAndChildInstances(t *testing.T) {
	expected, observed := identityFixture()
	// Explicit child/grandchild instance registrations remain separate from parents.
	expected[2].PID = 201
	expected[2].Name = "child"
	expected[2].StartIdentity = "child-start"
	observed[2].PID = 201
	observed[2].Name = "child"
	observed[2].StartIdentity = "child-start"
	expected[3].PID = 202
	expected[3].Name = "grandchild"
	expected[3].StartIdentity = "grandchild-start"
	observed[3].PID = 202
	observed[3].Name = "grandchild"
	observed[3].StartIdentity = "grandchild-start"
	expected = append(expected, ExpectedIdentityFlow{ID: "shared", Shared: true, UnknownReason: "shared_socket"})
	observed = append(observed, IdentityObservation{FlowID: "shared", UnknownReason: "shared_socket", Terminal: true})
	if r := CheckIdentityRun(expected, observed, true); r.Status != Passed {
		t.Fatalf("predeclared shared unknown: %+v", r)
	}
	observed[100].Terminal = false
	if r := CheckIdentityRun(expected, observed, true); r.Status != Unverified {
		t.Fatal("shared flow terminal facts omitted")
	}
	observed[100].Terminal = true
	observed[100].PID = 999
	if r := CheckIdentityRun(expected, observed, true); r.Status != Failed {
		t.Fatal("shared socket attributed to forwarder")
	}
	for i := range observed {
		observed[i].SocketEvidenceID = ""
		observed[i].PID = 0
	}
	if r := CheckIdentityRun(expected, observed, true); r.Status == Passed {
		t.Fatal("all unknown bypassed identifiable oracle")
	}
}
