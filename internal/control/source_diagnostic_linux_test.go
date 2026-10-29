//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
)

func TestSourcePlanDiagnosticRefusalsAreSpecificAndReadOnly(t *testing.T) {
	core := sourcePlanTestCore(t)
	for _, kind := range []string{"required-region", "manual-missing", "manual-region", "fixed-missing", "target-group", "source-reference", "active-unset", "follow-missing", "follow-cycle", "source-snapshot", "source-definition", "malformed-private"} {
		t.Run(kind, func(t *testing.T) {
			c, p, seed, intent := sourcePlanTestFixture(t, core)
			groups := intent["groups"].([]any)
			group := groups[0].(map[string]any)
			want, field := "", ""
			class := ErrPlan
			switch kind {
			case "required-region":
				group["region"] = "JP"
				want, field = "required_group_no_region_match", "groups[US].region"
			case "manual-missing", "manual-region":
				group["strategy"] = "manual"
				delete(group, "probe")
				group["selected_node_id"] = "missing-node"
				want, field = "selected_node_missing", "groups[US].selected_node_id"
				if kind == "manual-region" {
					group["selected_node_id"] = seed.snapshot.Nodes[0].ID
					group["region"] = "JP"
					want = "selected_node_region_mismatch"
				}
			case "fixed-missing":
				intent["default_target"] = map[string]any{"kind": "node", "subscription_id": "source-a", "node_id": "missing-node"}
				want, field = "pinned_node_missing", "default_target"
			case "target-group":
				intent["default_target"] = map[string]any{"kind": "group", "group_id": "absent"}
				want, field = "target_group_missing", "default_target"
			case "source-reference":
				intent["selection"] = map[string]any{"active_subscription": "absent"}
				want, field = "source_reference_missing", "selection.active_subscription"
			case "active-unset":
				intent["selection"] = map[string]any{"active_subscription": nil}
				want, field = "active_source_unset", "groups[US].source"
			case "follow-missing":
				groups[1].(map[string]any)["follows"] = "absent"
				want, field = "follow_target_missing", "groups[follow].follows"
			case "follow-cycle":
				group["strategy"] = "follow"
				delete(group, "source")
				delete(group, "probe")
				group["follows"] = "follow"
				want, field = "follow_cycle", "groups[US].follows"
			case "source-snapshot":
				missing := seed.entry.Context.Source
				missing.ID = "source-b"
				intent["subscriptions"] = []any{seed.entry.Context.Source, missing}
				intent["selection"] = map[string]any{"active_subscription": "source-b"}
				class = config.ErrReadUnavailable
				want, field = "source_snapshot_missing", "subscriptions[source-b]"
			case "source-definition":
				changed := seed.entry.Context.Source
				changed.Source.Path += ".changed"
				intent["subscriptions"] = []any{changed}
				class = config.ErrReadUnavailable
				want, field = "source_definition_changed", "subscriptions[source-a]"
			case "malformed-private":
				group["probe"].(map[string]any)["url"] = "ftp://private-probe-secret.example/"
			}
			before := coordinatorTree(t, p)
			plan, err := c.PlanSources(context.Background(), planRaw(t, intent))
			if !errors.Is(err, class) || plan.Digest() != "" || !reflect.DeepEqual(before, coordinatorTree(t, p)) {
				t.Fatal("diagnostic changed refusal/created Plan/wrote", err)
			}
			reason, location, ok := SourcePlanFailureDetails(err)
			if (want != "") != ok || reason != want || location != field {
				t.Fatalf("reason/location: %q %q %v; want %q %q", reason, location, ok, want, field)
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				value, expected := fmt.Sprintf(format, err), class.Error()
				if want == "" {
					expected = fmt.Sprintf(format, class)
				}
				if value != expected || strings.Contains(value, "private-probe-secret") || strings.Contains(value, seed.entry.Context.Source.Source.Path) {
					t.Fatal("error formatting exposed diagnostic inputs")
				}
			}
		})
	}
}

func TestSourcePlanDiagnosticOnlyRecognizesSafeTypedErrors(t *testing.T) {
	for _, err := range []error{nil, ErrPlan, fmt.Errorf("%s", "required_group_empty"), sourcePlanDetail(ErrPlan, "private-body", "groups[A]"), sourcePlanDetail(ErrPlan, "required_group_empty", strings.Repeat("x", 513))} {
		if reason, field, ok := SourcePlanFailureDetails(err); ok || reason != "" || field != "" {
			t.Fatal("untrusted/plain error became diagnostic")
		}
	}
	err := sourcePlanDetail(ErrPlan, "required_group_empty", "groups[A]")
	if reason, field, ok := SourcePlanFailureDetails(fmt.Errorf("wrapped: %w", err)); !ok || reason != "required_group_empty" || field != "groups[A]" {
		t.Fatal("safe wrapped diagnostic lost")
	}
}
