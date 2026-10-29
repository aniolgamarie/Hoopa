//go:build linux && amd64

package cli

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDoctorCLIMissingInstanceAndMachineStayReadOnly(t *testing.T) {
	paths := cliPaths(t)
	before := cliTree(t, paths)
	for _, args := range [][]string{{"doctor"}, {"api", "v1", "doctor.get"}} {
		input := ""
		if args[0] == "api" {
			input = cliRequest("doctor-correlation", "doctor.get", `{}`)
		}
		code, response, _ := cliResponse(t, args, strings.NewReader(input))
		if code != 0 || string(response["ok"]) != "true" {
			t.Fatal("missing instance diagnostic failed", code, string(response["error"]))
		}
		var data struct {
			SchemaVersion int                                                             `json:"schema_version"`
			Checks        []struct{ ID, Result, Evidence, Reason, Recommendation string } `json:"checks"`
		}
		if json.Unmarshal(response["data"], &data) != nil || data.SchemaVersion != 1 || len(data.Checks) != 6 {
			t.Fatal("diagnostic shape")
		}
		if data.Checks[0].ID != "state" || data.Checks[0].Result != "unavailable" || data.Checks[0].Reason != "state_missing_or_incomplete" {
			t.Fatal("missing state represented as healthy")
		}
		if args[0] == "api" && string(response["request_id"]) != `"doctor-correlation"` {
			t.Fatal("correlation identity")
		}
		if !reflect.DeepEqual(before, cliTree(t, paths)) {
			t.Fatal("doctor initialized state")
		}
	}
	for _, args := range [][]string{{"doctor", "extra"}, {"doctor", "--repair"}} {
		code, _, _ := cliResponse(t, args, strings.NewReader(""))
		if code != 2 {
			t.Fatal("invalid doctor arguments")
		}
	}
}

func TestDoctorCLIExistingServiceUsesSameQuery(t *testing.T) {
	paths := cliPaths(t)
	_, _, stop := subscriptionCLIService(t, paths)
	defer stop()
	before := cliTree(t, paths)
	code, response, _ := cliResponse(t, []string{"doctor"}, strings.NewReader(""))
	if code != 0 || string(response["ok"]) != "true" {
		t.Fatal("online doctor", code, string(response["error"]))
	}
	if !reflect.DeepEqual(before, cliTree(t, paths)) {
		t.Fatal("online doctor changed protected state")
	}
}
