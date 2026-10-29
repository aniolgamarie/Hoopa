//go:build linux && amd64

package control

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"

	"hoopa/internal/config"
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

// InitializeLaunchHistory is an explicit fixture/init primitive, never a query
// or ordinary service-open side effect. It grants no launch-stage write handle.
func (c *Coordinator) InitializeLaunchHistory(ctx context.Context, expected config.Revision) error {
	if c == nil || c.coordinatorState == nil {
		return config.ErrClosed
	}
	if ctx == nil {
		return ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := config.ParseID(string(expected)); err != nil {
		return ErrRequest
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return config.ErrClosed
	}
	active, err := c.store.ReadActive()
	if err != nil {
		return err
	}
	if active.Revision != expected {
		return ErrConflict
	}
	return c.store.InitializeLaunchHistory()
}

// InitializeLocal owns a short-lived configuration writer. It never delegates
// to the management RPC or runtime APIs; callbacks reject all Apply attempts.
func InitializeLocal(ctx context.Context, paths config.Paths, options InitOptions) (report InitReport, err error) {
	report = InitReport{SchemaVersion: 1, InputState: "incomplete", PreparedComponents: []string{}, Missing: []string{}, MissingAppIDs: []config.ID{}, Dependencies: []InitDependency{}, NextSteps: []string{}}
	if ctx == nil {
		return report, ErrRequest
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	input, err := PrepareInitInput(options)
	if err != nil {
		return report, err
	}
	if len(input.missing) != 0 {
		report.Missing = append(report.Missing, input.missing...)
		report.NextSteps = append(report.NextSteps, "provide_complete_intent")
		return report, nil
	}
	if len(input.canonical) == 0 {
		return report, ErrRequest
	}
	if e := config.ValidateDraft(input.canonical); e != nil {
		return report, ErrRequest
	}
	options = input.options
	report.InputState = "complete"
	for _, item := range []struct {
		kind string
		ref  *config.BinaryReference
	}{{"mihomo", &input.intent.Core.Mihomo}, {"gost", input.intent.Core.Gost}} {
		if item.ref == nil {
			continue
		}
		dependency, e := inspectInitDependency(ctx, item.kind, *item.ref)
		if e != nil {
			return report, e
		}
		report.Dependencies = append(report.Dependencies, dependency)
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	c, err := OpenCoordinator(paths, func(json.RawMessage, []PlanInput) error { return ErrCapabilityUnsupported }, func(context.Context, Plan) (CurrentPlanInputs, error) {
		return CurrentPlanInputs{}, ErrCapabilityUnsupported
	})
	if err != nil {
		return report, err
	}
	defer func() {
		err = errors.Join(err, c.Close())
		if err != nil {
			report.Prepared = false
		}
		if errors.Is(err, config.ErrSourceSettingsConflict) || errors.Is(err, config.ErrAppBindingConflict) {
			err = errors.Join(ErrConflict, err)
		}
	}()
	active, err := c.store.ReadActive()
	if err != nil {
		return report, err
	}
	revision := active.Revision
	report.Revision = &revision
	report.PreparedComponents = append(report.PreparedComponents, "store")
	// Invalid existing draft authority is never silently repaired by init.
	if _, _, e := c.store.ReadDraft(); e != nil && !errors.Is(e, os.ErrNotExist) {
		return report, e
	}
	if err = c.store.CheckLaunchHistoryInitialization(); err != nil {
		return report, err
	}
	settings, hasSettings, err := c.prepareInitSettings(options)
	if err != nil {
		return report, err
	}
	bindings, err := c.appBindingInventoryLocked(ctx) // This local coordinator has no concurrent owner.
	if err != nil {
		return report, err
	}
	if !hasSettings && len(bindings) != 0 {
		return report, ErrRecoveryRequired
	}
	requested := []AppEntryBinding{}
	if hasSettings {
		for _, app := range input.intent.Apps {
			if app.Mode != "managed" || app.Metering {
				continue
			}
			port := options.AppPorts[app.ID]
			if old, exists := bindings[app.ID]; exists {
				entry, e := old.SensitiveEntry()
				if e != nil {
					return report, e
				}
				if port != 0 && port != entry.Port {
					return report, config.ErrAppBindingConflict
				}
				requested = append(requested, old)
				continue
			}
			if port == 0 {
				continue
			}
			entry, e := initEntry(config.AppEntryAlias(app.ID), port)
			if e != nil {
				return report, e
			}
			binding, e := NewAppEntryBinding(app.ID, entry, settings)
			if e != nil {
				return report, e
			}
			bindings[app.ID] = binding
			requested = append(requested, binding)
		}
		if len(bindings) > config.MaxAppBindings {
			return report, config.ErrAppBindingInventoryBound
		}
		if err = validateAppBindingInventory(ctx, settings, bindings); err != nil {
			return report, err
		}
		// All currently known immutable conflicts were checked before writes.
		if _, err = c.InitializeSourceSettings(ctx, revision, settings); err != nil {
			return report, err
		}
		report.PreparedComponents = append(report.PreparedComponents, "source_settings")
		for _, binding := range requested {
			if _, err = c.InitializeAppEntry(ctx, revision, binding); err != nil {
				return report, err
			}
			report.AppEntriesReady++
		}
		if len(requested) != 0 {
			report.PreparedComponents = append(report.PreparedComponents, "app_entries")
		}
	}
	missingEntries, metered := false, false
	for _, app := range input.intent.Apps {
		if app.Mode != "managed" {
			continue
		}
		if app.Metering {
			metered = true
			report.MissingAppIDs = append(report.MissingAppIDs, app.ID)
			continue
		}
		if _, exists := bindings[app.ID]; !hasSettings || !exists {
			missingEntries = true
			report.MissingAppIDs = append(report.MissingAppIDs, app.ID)
		}
	}
	if !hasSettings {
		report.Missing = append(report.Missing, "source_settings")
		report.NextSteps = append(report.NextSteps, "provide_source_ports")
	}
	if missingEntries {
		report.Missing = append(report.Missing, "app_entries")
		report.NextSteps = append(report.NextSteps, "provide_app_ports")
	}
	if metered {
		report.Missing = append(report.Missing, "metering_backend")
		report.NextSteps = append(report.NextSteps, "metering_not_supported")
	}
	if err = c.InitializeLaunchHistory(ctx, revision); err != nil {
		return report, err
	}
	report.PreparedComponents = append(report.PreparedComponents, "launch_history")
	if err = ctx.Err(); err != nil {
		return report, err
	}
	sha, err := c.store.PublishDraft(input.canonical)
	if err != nil {
		return report, err
	}
	report.DraftSHA256 = &sha
	report.PreparedComponents = append(report.PreparedComponents, "draft")
	report.Prepared = true
	report.NextSteps = append(report.NextSteps, "plan_and_apply")
	return report, nil
}

func (c *Coordinator) prepareInitSettings(options InitOptions) (SourceSettings, bool, error) {
	raw, sha, err := c.store.ReadSourceSettings()
	if err == nil {
		settings, e := DecodeSourceSettings(raw)
		if e != nil || settings.Digest() != sha {
			return SourceSettings{}, false, config.ErrReadUnavailable
		}
		runtime, e := settings.Runtime()
		if e != nil {
			return SourceSettings{}, false, e
		}
		if options.ControllerPort != 0 && (runtime.Controller != net.JoinHostPort("127.0.0.1", strconv.Itoa(int(options.ControllerPort))) || runtime.Entry.Port != options.SharedPort) {
			return SourceSettings{}, false, config.ErrSourceSettingsConflict
		}
		return settings, true, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return SourceSettings{}, false, err
	}
	active, e := c.store.ReadActive()
	if e != nil {
		return SourceSettings{}, false, e
	}
	if e = c.missingSourceSettingsAllowedLocked(active); e != nil {
		return SourceSettings{}, false, e
	}
	if options.ControllerPort == 0 {
		return SourceSettings{}, false, nil
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return SourceSettings{}, false, err
	}
	secret, err := initSecret()
	if err != nil {
		return SourceSettings{}, false, err
	}
	entry, err := initEntry("shared-entry", options.SharedPort)
	if err != nil {
		return SourceSettings{}, false, err
	}
	settings, err := NewSourceSettings(key, policy.RuntimeInput{Controller: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(options.ControllerPort))), Secret: secret, Entry: entry})
	return settings, err == nil, err
}

func initSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
func initEntry(name string, port uint16) (profile.Entry, error) {
	username, err := initSecret()
	if err != nil {
		return profile.Entry{}, err
	}
	password, err := initSecret()
	if err != nil {
		return profile.Entry{}, err
	}
	return profile.Entry{Name: name, Type: "mixed", Listen: "127.0.0.1", Port: port, Users: []profile.User{{Username: username, Password: password}}}, nil
}

func inspectInitDependency(ctx context.Context, kind string, ref config.BinaryReference) (InitDependency, error) {
	out := InitDependency{Kind: kind, State: "unavailable", Reason: "not_readable"}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	f, err := os.OpenFile(ref.Path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return out, nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, nil
	}
	if !info.Mode().IsRegular() {
		out.Reason = "not_regular"
		return out, nil
	}
	const max = 128 << 20
	if info.Size() < 0 || info.Size() > max {
		out.Reason = "too_large"
		return out, nil
	}
	h := sha256.New()
	buffer := make([]byte, 32<<10)
	total := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		n, e := f.Read(buffer)
		total += int64(n)
		if total > max {
			out.Reason = "too_large"
			return out, nil
		}
		if n > 0 {
			_, _ = h.Write(buffer[:n])
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return out, nil
		}
	}
	if total != info.Size() {
		return out, nil
	}
	sha := hex.EncodeToString(h.Sum(nil))
	out.SHA256 = &sha
	if sha != ref.SHA256 {
		out.State, out.Reason = "mismatch", "hash_mismatch"
		return out, nil
	}
	out.State, out.Reason = "available", "hash_matched"
	version := ref.Version
	out.Version = &version
	return out, nil
}
