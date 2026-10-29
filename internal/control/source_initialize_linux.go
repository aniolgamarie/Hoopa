//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"os"

	"hoopa/internal/config"
)

// InitializeSourceSettings is an explicit local initialization primitive. It
// receives an already constructed private key/profile; it generates no missing
// value, fetches no provider and starts no process. The later init CLI supplies
// its own complete input validation. This is not a JSON update operation.
func (c *Coordinator) InitializeSourceSettings(ctx context.Context, expected config.Revision, settings SourceSettings) (string, error) {
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
	raw, err := settings.SensitiveBytes()
	if err != nil {
		return "", ErrSourceSettings
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
	prior, priorSHA, err := c.store.ReadSourceSettings()
	if err == nil {
		old, err := DecodeSourceSettings(prior)
		if err != nil || old.Digest() != priorSHA {
			return "", config.ErrReadUnavailable
		}
		if old.Digest() != settings.Digest() {
			return "", config.ErrSourceSettingsConflict
		}
	} else {
		if !errors.Is(err, os.ErrNotExist) {
			return "", config.ErrReadUnavailable
		}
		if err := c.missingSourceSettingsAllowedLocked(active); err != nil {
			return "", err
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return c.store.PublishSourceSettings(raw)
}

// Caller owns the coordinator. Missing setup must not erase prior identity history.
func (c *Coordinator) missingSourceSettingsAllowedLocked(active config.ActivePointer) error {
	// Once runtime configuration or a source update has existed, missing
	// settings are a recovery problem. Do not silently give old pins a new key.
	if active.Generation != nil || active.OperationID != nil || active.DesiredRuntime != "stopped" {
		return ErrRecoveryRequired
	}
	// Prepared or retained app entries bind the installation key even if no
	// application has yet been applied. Losing settings cannot rekey them.
	bindings, err := c.store.ListAppBindings()
	if err != nil {
		return config.ErrReadUnavailable
	}
	retained, err := c.store.ReadAppBindingHistory()
	if err != nil {
		return config.ErrReadUnavailable
	}
	if len(bindings) != 0 || len(retained) != 0 {
		return ErrRecoveryRequired
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return config.ErrReadUnavailable
	}
	for _, id := range ids {
		op, err := c.journal.Get(id)
		if err != nil {
			return config.ErrReadUnavailable
		}
		if !terminal(op.Record.Phase) || op.Record.Identity.Operation == "subscription.update" || op.Record.Identity.Operation == "config.apply" || op.Record.Identity.Operation == "runtime.start" || op.Record.Identity.Operation == "runtime.stop" {
			return ErrRecoveryRequired
		}
	}
	history, err := c.store.HasSourceAssets()
	if err != nil {
		return config.ErrReadUnavailable
	}
	if history {
		return ErrRecoveryRequired
	}
	return nil
}
