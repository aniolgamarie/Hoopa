//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func doctorCheck(t *testing.T, report DoctorReport, id string) DoctorCheck {
	t.Helper()
	if report.SchemaVersion != 1 || report.ObservedAt.IsZero() || len(report.Checks) != 6 {
		t.Fatal("incomplete diagnostic projection")
	}
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatal("missing diagnostic", id)
	return DoctorCheck{}
}

func TestDoctorMissingAndExistingStateNeverWrites(t *testing.T) {
	paths, err := config.ResolvePaths(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	before := coordinatorTree(t, paths)
	report, err := ReadDoctor(context.Background(), paths)
	if err != nil || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("missing state query wrote or failed", err)
	}
	if got := doctorCheck(t, report, "state"); got.Result != "unavailable" || got.Reason != "state_missing_or_incomplete" {
		t.Fatal(got)
	}
	if got := doctorCheck(t, report, "core"); got.Result != "unavailable" || got.Evidence != "not_observed" {
		t.Fatal("missing core fabricated", got)
	}
	c, paths := coordinatorFixture(t, nil)
	before = coordinatorTree(t, paths)
	for _, read := range []func(context.Context) (DoctorReport, error){c.Doctor, func(ctx context.Context) (DoctorReport, error) { return ReadDoctor(ctx, paths) }} {
		report, err = read(context.Background())
		if err != nil || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
			t.Fatal("diagnostic mutated protected state", err)
		}
		if got := doctorCheck(t, report, "configuration"); got.Result != "warning" || got.Reason != "no_committed_configuration" {
			t.Fatal(got)
		}
		if got := doctorCheck(t, report, "core"); got.Result != "pass" || got.Reason != "empty_configuration" {
			t.Fatal(got)
		}
		if got := doctorCheck(t, report, "collector"); got.Result != "unavailable" || got.Reason != "not_implemented" {
			t.Fatal("collector fabricated", got)
		}
	}
	if err = os.Remove(filepath.Join(paths.State, "active.json")); err != nil {
		t.Fatal(err)
	}
	before = coordinatorTree(t, paths)
	report, err = ReadDoctor(context.Background(), paths)
	if err != nil || doctorCheck(t, report, "state").Reason != "state_missing_or_incomplete" || !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("lost authority was repaired or called pristine", err)
	}
}

func TestDoctorCancellationAndUnsafeState(t *testing.T) {
	c, paths := coordinatorFixture(t, nil)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	before := coordinatorTree(t, paths)
	for _, read := range []func(context.Context) (DoctorReport, error){c.Doctor, func(ctx context.Context) (DoctorReport, error) { return ReadDoctor(ctx, paths) }} {
		if _, err := read(nil); !errors.Is(err, ErrRequest) {
			t.Fatal("nil context", err)
		}
		if _, err := read(canceled); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation disguised as report", err)
		}
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("cancelled diagnostic wrote")
	}
	if err := os.Chmod(paths.State, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(paths.State, 0700) })
	before = coordinatorTree(t, paths)
	if _, err := ReadDoctor(context.Background(), paths); err == nil {
		t.Fatal("unsafe state became missing-success")
	}
	if !reflect.DeepEqual(before, coordinatorTree(t, paths)) {
		t.Fatal("diagnostic repaired permissions")
	}
}

func TestDoctorProjectionDoesNotExposeUnknownEvidence(t *testing.T) {
	// Synthetic status projections do not establish a real core or process.
	s := ServiceStatus{Service: "unavailable", Core: CoreStatus{State: "unknown", Reason: "activation_requires_management"}, Collector: "unavailable", PendingTotal: 1}
	report := doctorProjection(s)
	if got := doctorCheck(t, report, "core"); got.Result != "unavailable" || got.Evidence != "not_observed" {
		t.Fatal(got)
	}
	if got := doctorCheck(t, report, "operations"); got.Result != "warning" || got.Evidence != "durable_operation_journal" {
		t.Fatal(got)
	}
	s.Core.Reason = "private-controller-secret"
	raw, err := json.Marshal(doctorProjection(s))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private-controller-secret") {
		t.Fatal("diagnostic echoed unbounded reason")
	}
}

func TestReadOnlyOperationClassification(t *testing.T) {
	for _, op := range []string{"status.get", "doctor.get", "operation.get", "subscription.list", "config.plan"} {
		if !IsReadOnlyOperation(op) {
			t.Fatal(op)
		}
	}
	for _, op := range []string{"config.apply", "runtime.start", "runtime.stop", "service.stop", "subscription.update", "operation.recover", "unknown", "usage.query", "Status.Get"} {
		if IsReadOnlyOperation(op) {
			t.Fatal("unimplemented/mutation classified readonly", op)
		}
	}
}
