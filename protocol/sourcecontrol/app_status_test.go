package sourcecontrol

import (
	"reflect"
	"testing"
)

// The additional v2 status profile describes a candidate wire shape only.
// Installation approval remains an exact binary/evidence registry decision.
func TestAppRoutingStatusRequiresExactProfile(t *testing.T) {
	s := fixtureStatus()
	s.Mode = "basic-app-routing"
	s.Capabilities = []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "ordered-app-rules-v1", "probe-qualified-groups-v1", "unknown-ip-reject-v1"}
	got, err := DecodeStatus(wire(t, s))
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatal("candidate profile rejected", err)
	}
	for _, mode := range []string{"basic-sources", "basic-app-routing", "app-routing"} {
		for _, caps := range [][]string{fixtureStatus().Capabilities, s.Capabilities, append(append([]string{}, s.Capabilities...), "process-identity-v1")} {
			v := s
			v.Mode = mode
			v.Capabilities = caps
			valid := mode == "basic-sources" && reflect.DeepEqual(caps, fixtureStatus().Capabilities) || mode == "basic-app-routing" && reflect.DeepEqual(caps, s.Capabilities)
			_, err := DecodeStatus(wire(t, v))
			if (err == nil) != valid {
				t.Fatalf("mode=%s caps=%v err=%v", mode, caps, err)
			}
		}
	}
}
