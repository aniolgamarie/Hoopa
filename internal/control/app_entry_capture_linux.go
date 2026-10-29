//go:build linux && amd64

package control

import (
	"context"

	"hoopa/internal/config"
)

// Caller holds c.mu. Capture only declared managed, unmetered inputs, while
// checking continuity and uniqueness against all retained app bindings. A
// candidate without managed entries does not consume this private inventory.
// Unsupported modes/predicates are still rejected by complete compilation.
func (c *Coordinator) captureAppBindingsLocked(ctx context.Context, settings SourceSettings, apps []config.App) (map[config.ID][]byte, error) {
	out := map[config.ID][]byte{}
	for _, app := range apps {
		if app.Mode == "managed" && !app.Metering {
			out[app.ID] = nil
		}
	}
	if len(out) == 0 {
		return out, nil
	}
	all, err := c.appBindingInventoryLocked(ctx)
	if err != nil {
		return nil, err
	}
	if err = validateAppBindingInventory(ctx, settings, all); err != nil {
		return nil, err
	}
	for id := range out {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		binding, ok := all[id]
		if !ok {
			return nil, config.ErrReadUnavailable
		}
		raw, err := binding.SensitiveBytes()
		if err != nil {
			return nil, config.ErrReadUnavailable
		}
		out[id] = raw
	}
	return out, nil
}
