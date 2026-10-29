//go:build linux && amd64

package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
)

var ErrController = errors.New("CORE_CONTROLLER_UNVERIFIED")

const SnapshotFile = "controller-snapshot.json"

// Controller consumes private immutable bundle material. It never dials: the
// runtime ownership layer supplies the exact connection it has already proved.
type Controller struct {
	store         *config.Store
	capabilities  CapabilityRecord
	capabilitySHA string
}

func (Controller) String() string   { return "mihomo.Controller{private material}" }
func (Controller) GoString() string { return "mihomo.Controller{private material}" }

func NewController(store *config.Store, capability CapabilityRecord) (*Controller, error) {
	if store == nil {
		return nil, ErrController
	}
	hash, err := capability.Digest()
	if err != nil {
		return nil, err
	}
	capability.Capabilities = append([]string(nil), capability.Capabilities...)
	return &Controller{store: store, capabilities: capability, capabilitySHA: hash}, nil
}

type controllerMaterial struct {
	secret       string
	snapshotJSON []byte
	snapshotSHA  string
}

// The expectation is independent bundle validation material, never synthesized
// from today's core response. A future compiler must produce this exact file.
func (c *Controller) material(intent config.RunIntent) (controllerMaterial, error) {
	var out controllerMaterial
	if intent.Validate() != nil || intent.Binary != c.capabilities.Binary {
		return out, ErrCapability
	}
	bundle, err := c.store.ReadBundle(intent.Generation)
	if err != nil || bundle.ManifestSHA256 != intent.ManifestSHA256 || bundle.Manifest.CapabilitiesDigest != c.capabilitySHA {
		return out, ErrController
	}
	foundBinary, foundSnapshot := false, false
	var rawConfig []byte
	for _, binary := range bundle.Manifest.Binaries {
		if binary == intent.Binary {
			foundBinary = true
		}
	}
	for _, file := range bundle.Manifest.Files {
		if file.Name == SnapshotFile && file.Kind == "validation" {
			foundSnapshot = true
		}
		if file.Kind == "generated_config" {
			if rawConfig != nil || file.SHA256 != intent.ConfigSHA256 {
				return out, ErrController
			}
			rawConfig = bundle.Files[file.Name]
		}
	}
	if !foundBinary || !foundSnapshot || len(rawConfig) == 0 || len(rawConfig) > 65536 {
		return out, ErrController
	}
	var configSHA, controller string
	out.snapshotJSON, configSHA, controller, out.snapshotSHA, err = decodeControllerSnapshot(c.capabilities.Protocol, bundle.Files[SnapshotFile])
	if err != nil || configSHA != intent.ConfigSHA256 || controller != intent.Controller {
		return out, ErrController
	}
	out.secret, err = configurationSecret(rawConfig, intent.Controller)
	if err != nil {
		return controllerMaterial{}, err
	}
	return out, nil
}

// The core independently validates the entire restricted profile. This reader
// only extracts the two transport fields, rejecting duplicate root keys instead
// of choosing an ambiguous credential. Errors never incorporate input bytes.
func configurationSecret(raw []byte, controller string) (string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", ErrController
	}
	seen := map[string]bool{}
	var secret, address string
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return "", ErrController
		}
		seen[key] = true
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return "", ErrController
		}
		if key == "secret" && json.Unmarshal(value, &secret) != nil || key == "external-controller" && json.Unmarshal(value, &address) != nil {
			return "", ErrController
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return "", ErrController
	}
	if _, err = d.Token(); err != io.EOF || address != controller || len(secret) < 32 || len(secret) > 128 {
		return "", ErrController
	}
	for _, c := range []byte(secret) {
		if c < 33 || c > 126 || c == ':' {
			return "", ErrController
		}
	}
	return secret, nil
}

func statusMatches(status wire.Status, intent config.RunIntent, material controllerMaterial, expected CapabilityRecord) bool {
	// Wire validity permits several profiles on v2. It cannot establish the
	// installed profile: every observation must match the already-bound record.
	// Protocol is the wire schema version; the registry's own schema remains 1.
	if status.SchemaVersion != expected.Protocol || status.Mode != expected.Mode || len(status.Capabilities) != len(expected.Capabilities) {
		return false
	}
	for i, capability := range expected.Capabilities {
		if status.Capabilities[i] != capability {
			return false
		}
	}
	return status.RunID == string(intent.RunID) && status.ConfigGeneration == string(intent.Generation) && status.ConfigSHA256 == intent.ConfigSHA256 && status.BinaryVersion == intent.Binary.Version && status.SnapshotSHA256 == material.snapshotSHA
}

func (c *Controller) inspectSession(session *controllerSession, intent config.RunIntent, material controllerMaterial) (wire.Status, error) {
	var zero wire.Status
	readStatus := func() (wire.Status, error) {
		raw, err := session.read(c.controlPrefix() + "status")
		if err != nil {
			return zero, err
		}
		status, err := decodeControllerStatus(c.capabilities.Protocol, raw)
		if err != nil || !statusMatches(status, intent, material, c.capabilities) {
			return zero, ErrController
		}
		return status, nil
	}
	if _, err := readStatus(); err != nil {
		return zero, err
	}
	raw, err := session.read(c.controlPrefix() + "snapshot")
	if err != nil {
		return zero, err
	}
	actualRaw, _, _, _, err := decodeControllerSnapshot(c.capabilities.Protocol, raw)
	if err != nil {
		return zero, ErrController
	}
	if !bytes.Equal(actualRaw, material.snapshotJSON) {
		return zero, ErrController
	}
	return readStatus() // A history receipt cannot replace today's gate state.
}

// Inspect implements runtime.ControllerInspector. The supplied conn is already
// owner-proved, remains caller-owned, and is the only transport used. There is
// no environment proxy, HTTP redirect, retry, core startup or implicit repair.
func (c *Controller) Inspect(ctx context.Context, conn *net.TCPConn, intent config.RunIntent) (owned.ControllerObservation, error) {
	var out owned.ControllerObservation
	if ctx == nil || conn == nil || ctx.Err() != nil || strings.ContainsAny(intent.Controller, "\r\n") {
		return out, ErrController
	}
	material, err := c.material(intent)
	if err != nil {
		return out, err
	}
	session, closeSession, err := beginControllerSession(ctx, conn, intent.Controller, material.secret)
	if err != nil {
		return out, err
	}
	defer closeSession()
	status, err := c.inspectSession(session, intent, material)
	if err != nil || status.State != "closed" && status.State != "open" {
		return out, ErrController
	}
	return owned.ControllerObservation{RunID: config.RunID(status.RunID), Generation: config.Generation(status.ConfigGeneration), ConfigSHA256: status.ConfigSHA256, BinaryVersion: status.BinaryVersion, Gate: status.State}, nil
}
