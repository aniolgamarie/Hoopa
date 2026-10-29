package routingcandidate

import "encoding/json"

// CompileControlTemplate compiles an immutable control input whose route-map
// generation is assigned later by the controller's durable launch. It does not
// produce a runnable source manifest or observed runtime evidence.
func CompileControlTemplate(s Spec) (Candidate, error) {
	if s.Generation != "" {
		return Candidate{}, ErrCandidate
	}
	s.Generation = "control-template"
	candidate, err := Compile(s)
	if err != nil {
		return Candidate{}, ErrCandidate
	}
	var mapping routeMap
	if err = json.Unmarshal(candidate.RouteMap, &mapping); err != nil {
		return Candidate{}, ErrCandidate
	}
	mapping.ConfigGeneration = ""
	candidate.RouteMap, err = json.Marshal(mapping)
	if err != nil || len(candidate.Config)+len(candidate.RouteMap) > 8<<10 {
		return Candidate{}, ErrCandidate
	}
	return candidate, nil
}
