//go:build linux && amd64

package control

import (
	"reflect"

	"hoopa/internal/config"
)

type sourceHistoryEntry struct {
	Operation StoredOperation
	Context   sourceUpdateContext
	Receipt   sourceUpdateReceipt
}
type sourceHistory struct {
	Entries []sourceHistoryEntry
	Attempt map[config.ID]int
	Latest  map[config.ID]int
}

func (c *Coordinator) sourceSettingsLocked() (SourceSettings, error) {
	raw, sha, err := c.store.ReadSourceSettings()
	if err != nil {
		return SourceSettings{}, config.ErrReadUnavailable
	}
	s, err := DecodeSourceSettings(raw)
	if err != nil || s.Digest() != sha {
		return SourceSettings{}, config.ErrReadUnavailable
	}
	return s, nil
}

func (c *Coordinator) sourceEntryLocked(op StoredOperation, settings SourceSettings) (sourceHistoryEntry, error) {
	bad := func() (sourceHistoryEntry, error) { return sourceHistoryEntry{}, config.ErrReadUnavailable }
	raw, err := c.store.ReadSourceAsset(op.Record.Identity.Intent.CandidateDigest)
	if err != nil {
		return bad()
	}
	context, err := decodeSourceUpdateContext(raw)
	if err != nil || context.SettingsSHA256 != settings.Digest() {
		return bad()
	}
	r, err := decodeSourceUpdateRecord(op, context)
	if err != nil {
		return bad()
	}
	out := sourceHistoryEntry{Operation: op, Context: context, Receipt: r}
	return out, nil
}

// This is the sole projection of durable source attempts; orphan assets cannot
// enter it. The latest success and newest attempt are intentionally independent.
func (c *Coordinator) sourceHistoryLocked(settings SourceSettings) (sourceHistory, error) {
	bad := func() (sourceHistory, error) { return sourceHistory{}, config.ErrReadUnavailable }
	ids, err := c.journal.IDs()
	if err != nil {
		return bad()
	}
	h := sourceHistory{Entries: []sourceHistoryEntry{}, Attempt: map[config.ID]int{}, Latest: map[config.ID]int{}}
	sequences := map[config.ID]map[uint64]bool{}
	for _, id := range ids {
		op, err := c.journal.Get(id)
		if err != nil {
			return bad()
		}
		if op.Record.Identity.Operation != "subscription.update" {
			continue
		}
		entry, err := c.sourceEntryLocked(op, settings)
		if err != nil {
			return bad()
		}
		source, sequence := entry.Context.Source.ID, entry.Context.Sequence
		if sequences[source] == nil {
			sequences[source] = map[uint64]bool{}
		}
		if sequences[source][sequence] {
			return bad()
		}
		sequences[source][sequence] = true
		index := len(h.Entries)
		h.Entries = append(h.Entries, entry)
		if old, ok := h.Attempt[source]; !ok || h.Entries[old].Context.Sequence < sequence {
			h.Attempt[source] = index
		}
		if op.Record.Phase == Committed {
			if old, ok := h.Latest[source]; !ok || h.Entries[old].Context.Sequence < sequence {
				h.Latest[source] = index
			}
		}
	}
	for source, values := range sequences {
		if uint64(len(values)) != h.Entries[h.Attempt[source]].Context.Sequence {
			return bad()
		}
	}
	return h, nil
}

func (c *Coordinator) committedSourceIntentLocked(base config.ActivePointer) (*config.Intent, error) {
	if base.Generation == nil {
		return nil, nil
	}
	bundle, err := c.store.ReadBundle(*base.Generation)
	if err != nil || bundle.ManifestSHA256 != base.ManifestSHA256 {
		return nil, config.ErrReadUnavailable
	}
	var raw []byte
	for _, f := range bundle.Manifest.Files {
		if f.Kind == "intent" {
			if raw != nil {
				return nil, config.ErrReadUnavailable
			}
			raw = bundle.Files[f.Name]
		}
	}
	intent, err := config.ParseIntent(raw, config.IntentApply)
	if err != nil {
		return nil, config.ErrReadUnavailable
	}
	return &intent, nil
}

func sourceContextMatchesBase(context sourceUpdateContext, base config.ActivePointer) bool {
	return reflect.DeepEqual(context.Base, base)
}
