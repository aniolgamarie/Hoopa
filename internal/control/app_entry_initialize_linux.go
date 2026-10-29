//go:build linux && amd64

package control

import (
	"context"
	"encoding/json"
	"net"
	"strconv"

	"hoopa/internal/config"
	profile "hoopa/protocol/sourceprofile"
)

// AppEntryBinding is private immutable preparation input. It does not authorize
// a listener, change an applied profile or assert an original process identity.
type AppEntryBinding = config.AppBinding

func NewAppEntryBinding(id config.ID, entry profile.Entry, settings SourceSettings) (AppEntryBinding, error) {
	runtime, err := settings.Runtime()
	if err != nil {
		return AppEntryBinding{}, ErrSourceSettings
	}
	binding, err := config.NewAppBinding(id, settings.Digest(), entry)
	if err != nil {
		return AppEntryBinding{}, config.ErrAppBinding
	}
	entry, err = binding.SensitiveEntry()
	if err != nil {
		return AppEntryBinding{}, config.ErrAppBinding
	}
	// Full finite profile validation compares the actual shared/controller inputs,
	// including endpoint, entry-name and complete auth-pair collisions. It is pure.
	candidate := profile.Config{Mode: "rule", LogLevel: "silent", FindProcessMode: "off", Controller: runtime.Controller, Secret: runtime.Secret, Listeners: []profile.Entry{runtime.Entry, entry}, Proxies: []map[string]any{}, Groups: []profile.Group{}, Rules: []string{"MATCH,REJECT"}}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return AppEntryBinding{}, config.ErrAppBinding
	}
	if _, err = profile.Validate(raw, "app-settings-check"); err != nil {
		return AppEntryBinding{}, config.ErrAppBinding
	}
	return binding, nil
}

// InitializeAppEntry prepares one immutable local input under the same writer
// boundary as Apply. Expected revision and ALL retained entry identities are
// checked before publication. No port probe/listen, journal, runtime or active
// transition is involved; a later complete confirmed Apply is still required.
func (c *Coordinator) InitializeAppEntry(ctx context.Context, expected config.Revision, binding AppEntryBinding) (string, error) {
	if c == nil || c.coordinatorState == nil {
		return "", config.ErrClosed
	}
	if ctx == nil {
		return "", ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := config.ParseID(string(expected)); err != nil {
		return "", ErrRequest
	}
	raw, err := binding.SensitiveBytes()
	if err != nil {
		return "", config.ErrAppBinding
	}
	checked, err := config.DecodeAppBinding(raw)
	if err != nil {
		return "", config.ErrAppBinding
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return "", config.ErrClosed
	}
	active, err := c.store.ReadActive()
	if err != nil {
		return "", config.ErrReadUnavailable
	}
	if active.Revision != expected {
		return "", ErrConflict
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return "", config.ErrReadUnavailable
	}
	if checked.SettingsSHA256() != settings.Digest() {
		return "", config.ErrAppBinding
	}
	entry, err := checked.SensitiveEntry()
	if err != nil {
		return "", config.ErrAppBinding
	}
	validated, err := NewAppEntryBinding(checked.AppID(), entry, settings)
	if err != nil || validated.Digest() != checked.Digest() {
		return "", config.ErrAppBinding
	}
	byID, err := c.appBindingInventoryLocked(ctx)
	if err != nil {
		return "", err
	}
	if old, exists := byID[checked.AppID()]; exists {
		if old.Digest() != checked.Digest() {
			return "", config.ErrAppBindingConflict
		}
	} else if len(byID) == config.MaxAppBindings {
		return "", config.ErrAppBindingInventoryBound
	}
	byID[checked.AppID()] = checked
	if err = validateAppBindingInventory(ctx, settings, byID); err != nil {
		return "", err
	}
	return c.store.PublishAppBinding(checked)
}

// Caller holds c.mu. Retained bundles establish continuity independently of the
// mutable live credential inventory. Never reconstruct a missing input here.
func (c *Coordinator) appBindingInventoryLocked(ctx context.Context) (map[config.ID]AppEntryBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	live, err := c.store.ListAppBindings()
	if err != nil {
		return nil, config.ErrReadUnavailable
	}
	history, err := c.store.ReadAppBindingHistory()
	if err != nil {
		return nil, config.ErrReadUnavailable
	}
	byID := make(map[config.ID]AppEntryBinding, len(live))
	for _, b := range live {
		byID[b.AppID()] = b
	}
	for _, old := range history {
		current, exists := byID[old.AppID()]
		if !exists || current.Digest() != old.Digest() {
			return nil, ErrRecoveryRequired
		}
	}
	return byID, nil
}

// Includes unused prepared bindings. The active core profile limit is smaller
// than this bounded inventory, so validate each entry against the real parent
// and compare the complete retained set without inventing a combined profile.
func validateAppBindingInventory(ctx context.Context, settings SourceSettings, byID map[config.ID]AppEntryBinding) error {
	runtime, err := settings.Runtime()
	if err != nil {
		return config.ErrReadUnavailable
	}
	names := map[string]bool{runtime.Entry.Name: true}
	endpoints := map[string]bool{runtime.Controller: true, net.JoinHostPort(runtime.Entry.Listen, strconv.Itoa(int(runtime.Entry.Port))): true}
	auth := map[profile.User]bool{runtime.Entry.Users[0]: true}
	for _, b := range byID {
		if err = ctx.Err(); err != nil {
			return err
		}
		if b.SettingsSHA256() != settings.Digest() {
			return ErrRecoveryRequired
		}
		e, err := b.SensitiveEntry()
		if err != nil {
			return config.ErrReadUnavailable
		}
		valid, err := NewAppEntryBinding(b.AppID(), e, settings)
		if err != nil || valid.Digest() != b.Digest() {
			return ErrRecoveryRequired
		}
		ep := net.JoinHostPort(e.Listen, strconv.Itoa(int(e.Port)))
		if names[e.Name] || endpoints[ep] || auth[e.Users[0]] {
			return config.ErrAppBindingConflict
		}
		names[e.Name], endpoints[ep], auth[e.Users[0]] = true, true, true
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return nil
}
