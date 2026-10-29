//go:build linux && amd64

package control

import "hoopa/internal/config"

// InitOptions is explicit local preparation input. It never authorizes a
// download, core start or application of a generation.
type InitOptions struct {
	MihomoPath     string
	GostPath       string
	Intent         []byte
	ControllerPort uint16
	SharedPort     uint16
	AppPorts       map[config.ID]uint16
}

func (InitOptions) String() string   { return "control.InitOptions{private input}" }
func (InitOptions) GoString() string { return "control.InitOptions{private input}" }

// InitInput owns a validated local declaration, or an explicit incomplete
// declaration which cannot be consumed by a writer.
type InitInput struct {
	options   InitOptions
	intent    config.Intent
	canonical []byte
	missing   []string
}

func (InitInput) String() string   { return "control.InitInput{private input}" }
func (InitInput) GoString() string { return "control.InitInput{private input}" }

type InitDependency struct {
	Kind    string  `json:"kind"`
	State   string  `json:"state"`
	Reason  string  `json:"reason"`
	SHA256  *string `json:"sha256"`
	Version *string `json:"version"`
}

// A completed preparation is not proof of a usable runtime or network access.
// Components and missing fields are finite diagnostic identifiers, not paths,
// credentials, shell commands or untrusted error strings.
type InitReport struct {
	SchemaVersion      int              `json:"schema_version"`
	InputState         string           `json:"input_state"`
	Prepared           bool             `json:"prepared"`
	DraftSHA256        *string          `json:"draft_sha256"`
	Revision           *config.Revision `json:"revision"`
	PreparedComponents []string         `json:"prepared_components"`
	Missing            []string         `json:"missing"`
	MissingAppIDs      []config.ID      `json:"missing_app_ids"`
	AppEntriesReady    int              `json:"app_entries_ready"`
	Dependencies       []InitDependency `json:"dependencies"`
	NextSteps          []string         `json:"next_steps"`
}
