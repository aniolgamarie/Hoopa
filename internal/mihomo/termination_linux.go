//go:build linux && amd64

package mihomo

import (
	"bytes"
	"context"
	"net"
	"reflect"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
)

// InspectTermination is a read-only proof for terminating THIS operation's
// candidate, not adoption or permission to activate a degraded process. The
// caller must already prove this exact connection's owner and match original
// intent/birth/registration bytes; this layer checks the repeated fields again.
// Ordinary Inspect remains unchanged. No connection is opened or mutation sent.
func (c *Controller) InspectTermination(ctx context.Context, conn *net.TCPConn, intent config.RunIntent, reg config.RunRegistration, registrationSHA string) (owned.ControllerObservation, error) {
	var zero owned.ControllerObservation
	if c == nil || c.store == nil || ctx == nil || ctx.Err() != nil || conn == nil || reg.Validate() != nil || !isDigest(registrationSHA) || reg.Gate != "closed" || reg.OperationID != intent.OperationID || reg.RunID != intent.RunID || reg.Generation != intent.Generation || reg.ManifestSHA256 != intent.ManifestSHA256 || reg.BinarySHA256 != intent.Binary.SHA256 || reg.ConfigSHA256 != intent.ConfigSHA256 || reg.NonceSHA256 != intent.NonceSHA256 || reg.Controller != intent.Controller {
		return zero, ErrController
	}
	material, e := c.material(intent)
	if e != nil {
		return zero, e
	}
	request := wire.Mutation{SchemaVersion: c.capabilities.Protocol, Operation: "activate", OperationID: string(reg.OperationID), RunID: string(intent.RunID), ConfigGeneration: string(intent.Generation), ConfigSHA256: intent.ConfigSHA256, SnapshotSHA256: material.snapshotSHA, RegistrationSHA256: registrationSHA}
	digest, e := controllerMutationDigest(c.capabilities.Protocol, request)
	if e != nil {
		return zero, ErrController
	}
	session, closeSession, e := beginControllerSession(ctx, conn, intent.Controller, material.secret)
	if e != nil {
		return zero, e
	}
	defer closeSession()
	status, e := c.inspectTerminationSession(session, intent, material, request, digest)
	if e != nil {
		return zero, e
	}
	return owned.ControllerObservation{RunID: config.RunID(status.RunID), Generation: config.Generation(status.ConfigGeneration), ConfigSHA256: status.ConfigSHA256, BinaryVersion: status.BinaryVersion, Gate: status.State}, nil
}

// Both observations must independently bind the expected activation receipt and
// remain identical. A transition during the three GETs fails closed; a later
// explicit recovery may observe again, but this helper never retries.
func (c *Controller) inspectTerminationSession(session *controllerSession, intent config.RunIntent, material controllerMaterial, request wire.Mutation, digest string) (wire.Status, error) {
	var zero wire.Status
	readStatus := func() (wire.Status, error) {
		raw, e := session.read(c.controlPrefix() + "status")
		if e != nil {
			return zero, e
		}
		status, e := decodeControllerStatus(c.capabilities.Protocol, raw)
		if e != nil || !statusMatches(status, intent, material, c.capabilities) || !validTerminationStatus(status, request, digest) {
			return zero, ErrController
		}
		return status, nil
	}
	first, e := readStatus()
	if e != nil {
		return zero, e
	}
	raw, e := session.read(c.controlPrefix() + "snapshot")
	if e != nil {
		return zero, e
	}
	actualRaw, _, _, _, e := decodeControllerSnapshot(c.capabilities.Protocol, raw)
	if e != nil {
		return zero, ErrController
	}
	if !bytes.Equal(actualRaw, material.snapshotJSON) {
		return zero, ErrController
	}
	last, e := readStatus()
	if e != nil {
		return zero, e
	}
	if !reflect.DeepEqual(first, last) {
		return zero, ErrController
	}
	return last, nil
}
func validTerminationStatus(status wire.Status, request wire.Mutation, digest string) bool {
	if status.Drain != nil {
		return false
	}
	if status.State == "closed" {
		return status.Activation == nil
	}
	expected := "opened"
	switch status.State {
	case "open":
	case "degraded":
		expected = "failed"
	default:
		return false
	}
	r := status.Activation
	return r != nil && r.OperationID == request.OperationID && r.RunID == request.RunID && r.RequestSHA256 == digest && r.Result == expected
}
