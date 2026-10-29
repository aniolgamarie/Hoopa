//go:build linux && amd64

package mihomo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"

	"hoopa/internal/config"
	owned "hoopa/internal/runtime"
	wire "hoopa/protocol/corecontrol"
)

var (
	ErrMutationConflict = errors.New("CORE_MUTATION_CONFLICT")
	ErrTransitionFailed = errors.New("CORE_TRANSITION_FAILED")
)

// MutationOutcome distinguishes an authenticated historical receipt from an
// independently verified current state. An error NEVER proves no remote effect.
// Receipt may remain available if post-observation fails; Current then is nil.
// Even a successful activate replay may have Current.State == "draining".
type MutationOutcome struct {
	Receipt *wire.Result
	Current *wire.Status
}

func (c *Controller) Activate(ctx context.Context, run *owned.OwnedRun, operation config.ID) (MutationOutcome, error) {
	return c.mutate(ctx, run, operation, "activate")
}

func (c *Controller) Drain(ctx context.Context, run *owned.OwnedRun, operation config.ID) (MutationOutcome, error) {
	return c.mutate(ctx, run, operation, "drain")
}

func (c *Controller) mutate(ctx context.Context, run *owned.OwnedRun, operation config.ID, action string) (out MutationOutcome, err error) {
	if c == nil {
		return out, ErrController
	}
	err = run.WithRegisteredController(ctx, c.store, operation, func(ctx context.Context, conn *net.TCPConn, intent config.RunIntent, reg config.RunRegistration, registrationSHA string) error {
		material, err := c.material(intent)
		if err != nil {
			return err
		}
		session, closeSession, err := beginControllerSession(ctx, conn, intent.Controller, material.secret)
		if err != nil {
			return err
		}
		defer closeSession()
		out, err = c.mutateSession(session, intent, material, reg, registrationSHA, action)
		return err
	})
	if err != nil {
		out.Current = nil // Includes failure of the final original-pidfd proof.
		return out, err
	}
	if out.Receipt.Result == "failed" {
		return out, ErrTransitionFailed
	}
	return out, nil
}

func mutationReceipt(status wire.Status, action string) *wire.Result {
	if action == "activate" {
		return status.Activation
	}
	return status.Drain
}

func (c *Controller) mutateSession(session *controllerSession, intent config.RunIntent, material controllerMaterial, reg config.RunRegistration, registrationSHA, action string) (out MutationOutcome, err error) {
	before, err := c.inspectSession(session, intent, material)
	if err != nil {
		return out, err
	}
	request := wire.Mutation{SchemaVersion: c.capabilities.Protocol, Operation: action, OperationID: string(reg.OperationID), RunID: string(intent.RunID), ConfigGeneration: string(intent.Generation), ConfigSHA256: intent.ConfigSHA256, SnapshotSHA256: material.snapshotSHA, RegistrationSHA256: registrationSHA}
	digest, err := controllerMutationDigest(c.capabilities.Protocol, request)
	if err != nil {
		return out, ErrController
	}
	previous := mutationReceipt(before, action)
	other := before.Activation
	if action == "activate" {
		other = before.Drain
	}
	if other != nil && other.OperationID == request.OperationID || previous != nil && (previous.OperationID != request.OperationID || previous.RequestSHA256 != digest) {
		return out, ErrMutationConflict
	}
	if previous == nil && (action == "activate" && (before.State != "closed" || reg.Gate != "closed") || action == "drain" && before.State != "closed" && before.State != "open") {
		return out, ErrMutationConflict
	}
	result, err := session.post(request, c.capabilities.Protocol)
	if err != nil {
		return out, err
	}
	if previous != nil && *previous != result {
		return out, ErrController
	}
	out.Receipt = &result
	after, err := c.inspectSession(session, intent, material)
	if err != nil {
		return out, err
	}
	history := mutationReceipt(after, action)
	if history == nil || *history != result {
		return out, ErrController
	}
	out.Current = &after
	return out, nil
}

// post sends at most one request on the caller's already-proved connection.
// No implicit retry follows a failed write, timeout, malformed or lost reply.
func (s *controllerSession) post(mutation wire.Mutation, protocol int) (wire.Result, error) {
	var zero wire.Result
	digest, err := controllerMutationDigest(protocol, mutation)
	if err != nil || s.ctx.Err() != nil {
		return zero, ErrController
	}
	raw, err := json.Marshal(mutation)
	if err != nil || len(raw) > 4096 {
		return zero, ErrController
	}
	prefix := "/hoopa/v1/"
	if protocol == 2 {
		prefix = "/hoopa/v2/"
	}
	request := &http.Request{Method: "POST", URL: &url.URL{Scheme: "http", Host: s.address, Path: prefix + mutation.Operation}, Host: s.address, Header: http.Header{"Authorization": {"Bearer " + s.secret}, "Content-Type": {"application/json"}}, ContentLength: int64(len(raw)), Body: io.NopCloser(bytes.NewReader(raw))}
	if request.Write(s.conn) != nil {
		return zero, ErrController
	}
	raw, code, err := s.responseCode(request, http.StatusOK, http.StatusServiceUnavailable)
	if err != nil {
		return zero, err
	}
	result, err := decodeControllerResult(protocol, raw)
	if err != nil || result.OperationID != mutation.OperationID || result.RunID != mutation.RunID || result.RequestSHA256 != digest || (result.Result == "failed") != (code == http.StatusServiceUnavailable) {
		return zero, ErrController
	}
	expected := "opened"
	if mutation.Operation == "drain" {
		expected = "draining"
	}
	if result.Result != "failed" && result.Result != expected {
		return zero, ErrController
	}
	return result, nil
}
