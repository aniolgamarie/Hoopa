//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path/filepath"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

// prepareSourceMaterialLocked is only for a new Apply after the admission layer
// has checked revision, complete inputs and consent. It creates preparation
// artifacts, never a run or operation. The ordinary Apply remains responsible
// for bundle publication/admission and the existing runtime workflow.
func (c *Coordinator) prepareSourceMaterialLocked(ctx context.Context, request Request, plan Plan, base config.ActivePointer, input BasicMaterial, boundary func(string) error) (checkedBasicMaterial, *config.RunID, error) {
	fail := func(err error) (checkedBasicMaterial, *config.RunID, error) { return checkedBasicMaterial{}, nil, err }
	if ctx == nil || input.Workdir != "" {
		return fail(ErrPlan)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	contents, err := checkBasicMaterialContents(ctx, plan, input)
	if err != nil {
		return fail(err)
	}
	executable, err := platform.OpenExecutable(ctx, contents.launch.Binary.Path, contents.launch.Binary.SHA256)
	if err != nil {
		return fail(config.ErrReadUnavailable)
	}
	if err = executable.Close(); err != nil {
		return fail(config.ErrReadUnavailable)
	}
	old, err := c.activeRunForApply(base, plan.Document().Impact.RuntimeAction, "")
	if err != nil {
		return fail(err)
	}
	if err = c.store.CheckOperationAdmission(); err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return fail(config.ErrReadUnavailable)
	}
	name := "launch-" + hex.EncodeToString(nonce[:])
	record := struct {
		Version       int                  `json:"schema_version"`
		Kind          string               `json:"kind"`
		OperationID   config.ID            `json:"operation_id"`
		RequestDigest string               `json:"request_digest"`
		PlanDigest    string               `json:"plan_digest"`
		Base          config.ActivePointer `json:"base"`
		Workdir       string               `json:"workdir_name"`
		ConfigSHA256  string               `json:"config_sha256"`
	}{1, "source-apply-workdir", request.OperationID(), request.Digest(), plan.Digest(), base, name, contents.launch.ConfigSHA256}
	raw, err := json.Marshal(record)
	if err != nil {
		return fail(ErrPlan)
	}
	if _, err = c.store.PublishSourceAsset(raw); err != nil {
		return fail(err)
	}
	if err = lifecycleCut(boundary, "apply-workdir-intent-published"); err != nil {
		return fail(err)
	}
	if err = ctx.Err(); err != nil {
		return fail(err)
	}
	var generated []byte
	for _, file := range contents.manifest.Files {
		if file.Kind == "generated_config" {
			generated = contents.files[file.Name]
		}
	}
	workdir, err := c.store.PrepareLaunchWorkdir(name, generated)
	if err != nil {
		return fail(err)
	}
	if err = lifecycleCut(boundary, "apply-workdir-created"); err != nil {
		return fail(err)
	}
	contents.launch.Workdir = workdir
	contents.launch.Arguments = []string{contents.launch.Binary.Path, "-d", workdir, "-f", filepath.Join(workdir, "config.json")}
	checked, err := verifyBasicMaterial(ctx, contents)
	if err != nil {
		return fail(err)
	}
	return checked, old, nil
}
