//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

// Explicit recovery reuses the existing journal call/target workflow. These
// checks do not acquire a URL, retry a download, install settings or change the
// active pointer. RestoreAsset is bounded local reproof under the recovery lock.
func (c *Coordinator) sourceRecoveryEntryLocked(target StoredOperation) (sourceHistoryEntry, error) {
	if c.sourceAlive != nil && c.sourceAlive(target.Record.Identity.OperationID) {
		return sourceHistoryEntry{}, ErrConflict
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return sourceHistoryEntry{}, err
	}
	entry, err := c.sourceEntryLocked(target, settings)
	if err != nil {
		return sourceHistoryEntry{}, err
	}
	if target.Record.Phase == Publishing || target.Record.Phase == Committed {
		asset, err := c.store.ReadSourceAsset(entry.Receipt.AssetSHA256)
		if err != nil {
			return sourceHistoryEntry{}, config.ErrReadUnavailable
		}
		key, err := settings.SensitiveIdentityKey()
		if err != nil {
			return sourceHistoryEntry{}, config.ErrReadUnavailable
		}
		snapshot, err := subscription.RestoreAsset(asset, subscription.ParseOptions{SourceID: entry.Context.Source.ID, IdentityKey: key, RegionRules: entry.Context.RegionRules})
		if err != nil || len(snapshot.Nodes) == 0 || string(snapshot.ID) != entry.Receipt.SnapshotID {
			return sourceHistoryEntry{}, config.ErrReadUnavailable
		}
	}
	return entry, nil
}

func (c *Coordinator) reconcileSourceUpdate(ctx context.Context, target StoredOperation, boundary func(string) error) (StoredOperation, error) {
	entry, err := c.sourceRecoveryEntryLocked(target)
	if err != nil {
		return target, err
	}
	if terminal(target.Record.Phase) {
		return target, nil
	}
	if err := ctx.Err(); err != nil {
		return target, err
	}
	receipt := entry.Receipt
	advance := func(phase Phase) error {
		next, r, err := c.advanceSourceLocked(target, receipt, phase)
		if err != nil {
			return errors.Join(errRecoveryPersistence, err)
		}
		target, receipt = next, r
		return lifecycleCut(boundary, "source-update-"+string(phase))
	}
	if target.Record.Phase == Publishing {
		if err := c.revalidateSourceLocked(ctx, entry.Context); err == nil {
			receipt.Validated = true
			err = advance(Committed)
			return target, err
		} else if ctx.Err() != nil {
			return target, ctx.Err()
		}
		receipt.Reason = "input_changed"
	} else if target.Record.Phase != RollingBack {
		receipt.Reason = "interrupted"
	}
	if target.Record.Phase != RollingBack {
		finished := time.Now().UTC().Format(time.RFC3339Nano)
		receipt.FinishedAt = &finished
		receipt.AssetSHA256, receipt.SnapshotID, receipt.Validated = "", "", false
		if err := advance(RollingBack); err != nil {
			return target, err
		}
	}
	if err := advance(RolledBack); err != nil {
		return target, err
	}
	return target, nil
}
