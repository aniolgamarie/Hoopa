//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/subscription"
)

var ErrSourceSnapshotExpired = errors.New("SOURCE_QUERY_SNAPSHOT_EXPIRED")

const sourcePageSize = 32

type SourceState struct {
	SourceID         config.ID `json:"subscription_id"`
	State            string    `json:"state"`
	Sequence         uint64    `json:"sequence"`
	Downloaded       bool      `json:"downloaded"`
	Validated        bool      `json:"validated"`
	LatestSnapshot   *string   `json:"latest_snapshot"`
	AppliedSnapshot  *string   `json:"applied_snapshot"`
	Pending          bool      `json:"pending"`
	LastAttempt      *string   `json:"last_attempt"`
	LastSuccess      *string   `json:"last_success"`
	NextDue          *string   `json:"next_due"`
	EffectiveNextDue *string   `json:"effective_next_due"`
	ErrorCode        *string   `json:"error_code"`
	Scheduled        bool      `json:"scheduled"`
	UpdateHours      *uint64   `json:"update_hours"`
}
type SourcePage struct {
	Revision   config.Revision `json:"revision"`
	ViewID     string          `json:"view_id"`
	ObservedAt time.Time       `json:"observed_at"`
	Total      int             `json:"total"`
	Sources    []SourceState   `json:"sources"`
	NextCursor *string         `json:"next_cursor"`
}
type sourceCursor struct {
	Version int       `json:"version"`
	View    string    `json:"view"`
	After   config.ID `json:"after"`
}

func parseSourceCursor(token string) (sourceCursor, error) {
	if len(token) > 512 {
		return sourceCursor{}, ErrRequest
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	var cursor sourceCursor
	if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.Version != 1 || !isHash(cursor.View) || !sourceContextID(string(cursor.After)) {
		return sourceCursor{}, ErrRequest
	}
	exact, err := json.Marshal(cursor)
	if err != nil || !bytes.Equal(exact, raw) || base64.RawURLEncoding.EncodeToString(raw) != token {
		return sourceCursor{}, ErrRequest
	}
	return cursor, nil
}
func SourceListCursor(r Request) (string, error) {
	if r.version != "v1" || r.operation != "subscription.list" {
		return "", ErrRequest
	}
	p, err := planObject(r.params)
	if err != nil || len(p) > 1 {
		return "", ErrRequest
	}
	if len(p) == 0 {
		return "", nil
	}
	var token string
	if json.Unmarshal(p["cursor"], &token) != nil {
		return "", ErrRequest
	}
	if _, err := parseSourceCursor(token); err != nil {
		return "", err
	}
	return token, nil
}

type sourceListAsset struct {
	entry sourceHistoryEntry
	raw   []byte
}
type sourceListRow struct {
	state           SourceState
	latest, applied *sourceListAsset
}

// Sources returns one immutable page of local observations. A cursor binds the
// full source journal/settings/runtime view; changes expire it rather than mix
// two views. It performs no downloads, writes, initialization or repair.
func (c *Coordinator) Sources(ctx context.Context, token string) (SourcePage, error) {
	if c == nil || c.coordinatorState == nil {
		return SourcePage{}, config.ErrClosed
	}
	if ctx == nil {
		return SourcePage{}, ErrRequest
	}
	if err := ctx.Err(); err != nil {
		return SourcePage{}, err
	}
	var cursor sourceCursor
	var err error
	if token != "" {
		cursor, err = parseSourceCursor(token)
		if err != nil {
			return SourcePage{}, err
		}
	}
	c.mu.Lock()
	page, rows, settings, err := c.captureSourcesLocked(cursor)
	c.mu.Unlock()
	if err != nil {
		return SourcePage{}, err
	}
	key, _ := settings.SensitiveIdentityKey()
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return SourcePage{}, err
		}
		for _, asset := range []*sourceListAsset{row.latest, row.applied} {
			if asset == nil {
				continue
			}
			s, err := subscription.RestoreAsset(asset.raw, subscription.ParseOptions{SourceID: row.state.SourceID, IdentityKey: key, RegionRules: asset.entry.Context.RegionRules})
			if err != nil || len(s.Nodes) == 0 || string(s.ID) != asset.entry.Receipt.SnapshotID {
				return SourcePage{}, config.ErrReadUnavailable
			}
		}
		page.Sources = append(page.Sources, row.state)
	}
	if err := ctx.Err(); err != nil {
		return SourcePage{}, err
	}
	return page, nil
}

func (c *Coordinator) captureSourcesLocked(cursor sourceCursor) (SourcePage, []sourceListRow, SourceSettings, error) {
	bad := func(err error) (SourcePage, []sourceListRow, SourceSettings, error) {
		return SourcePage{}, nil, SourceSettings{}, err
	}
	if c.store == nil {
		return bad(config.ErrClosed)
	}
	base, err := c.store.ReadActive()
	if err != nil {
		return bad(err)
	}
	ids, err := c.journal.IDs()
	if err != nil {
		return bad(err)
	}
	hasSource := false
	for _, id := range ids {
		op, err := c.journal.Get(id)
		if err != nil {
			return bad(err)
		}
		hasSource = hasSource || op.Record.Identity.Operation == "subscription.update"
	}
	settingsRaw, _, err := c.store.ReadSourceSettings()
	var settings SourceSettings
	history := sourceHistory{Entries: []sourceHistoryEntry{}, Attempt: map[config.ID]int{}, Latest: map[config.ID]int{}}
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) || hasSource || base.Generation != nil {
			return bad(config.ErrReadUnavailable)
		}
		assets, err := c.store.HasSourceAssets()
		if err != nil || assets {
			return bad(config.ErrReadUnavailable)
		}
	} else {
		settings, err = DecodeSourceSettings(settingsRaw)
		if err != nil {
			return bad(config.ErrReadUnavailable)
		}
		history, err = c.sourceHistoryLocked(settings)
		if err != nil {
			return bad(err)
		}
	}
	committed, err := c.committedSourceIntentLocked(base)
	if err != nil {
		return bad(err)
	}
	definitions := map[config.ID]config.Subscription{}
	if committed != nil {
		for _, source := range committed.Subscriptions {
			definitions[source.ID] = source
		}
	}
	known := map[config.ID]bool{}
	for id := range definitions {
		known[id] = true
	}
	for id := range history.Attempt {
		known[id] = true
	}
	names := make([]config.ID, 0, len(known))
	for id := range known {
		names = append(names, id)
	}
	sort.Slice(names, func(i, j int) bool { return names[i] < names[j] })
	type binding struct {
		ID   config.ID
		SHA  string
		Live bool
	}
	bindings := make([]binding, 0, len(history.Entries))
	for _, entry := range history.Entries {
		live := c.sourceAlive != nil && c.sourceAlive(entry.Context.OperationID)
		bindings = append(bindings, binding{entry.Context.OperationID, entry.Operation.SHA256, live})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	bound, _ := json.Marshal([]any{"hoopa.source-list.v1", base, settings.Digest(), bindings})
	view := hash(bound)
	if cursor.Version != 0 && cursor.View != view {
		return bad(ErrSourceSnapshotExpired)
	}
	start := 0
	if cursor.Version != 0 {
		at := sort.Search(len(names), func(i int) bool { return names[i] >= cursor.After })
		if at == len(names) || names[at] != cursor.After {
			return bad(ErrRequest)
		}
		start = at + 1
	}
	end := min(start+sourcePageSize, len(names))
	page := SourcePage{Revision: base.Revision, ViewID: view, ObservedAt: time.Now().UTC(), Total: len(names), Sources: []SourceState{}}
	if end < len(names) {
		raw, _ := json.Marshal(sourceCursor{1, view, names[end-1]})
		token := base64.RawURLEncoding.EncodeToString(raw)
		page.NextCursor = &token
	}
	var bundle config.StoredBundle
	if base.Generation != nil {
		bundle, err = c.store.ReadBundle(*base.Generation)
		if err != nil || bundle.ManifestSHA256 != base.ManifestSHA256 {
			return bad(config.ErrReadUnavailable)
		}
	}
	rows := make([]sourceListRow, 0, end-start)
	for _, id := range names[start:end] {
		row := sourceListRow{state: SourceState{SourceID: id, State: "never"}}
		if definition, ok := definitions[id]; ok && definition.UpdateHours != nil {
			hours := *definition.UpdateHours
			row.state.UpdateHours = &hours
			row.state.Scheduled = true
			due, err := sourceEffectiveDue(definition, history)
			if err != nil {
				return bad(config.ErrReadUnavailable)
			}
			if !due.IsZero() {
				stamp := due.Format(time.RFC3339Nano)
				row.state.EffectiveNextDue = &stamp
			}
		}
		if at, ok := history.Attempt[id]; ok {
			entry := history.Entries[at]
			r := entry.Receipt
			row.state.Sequence, row.state.Downloaded, row.state.Validated = r.Sequence, r.Downloaded, r.Validated
			start := entry.Context.StartedAt
			row.state.LastAttempt = &start
			row.state.NextDue = entry.Context.NextDue
			switch entry.Operation.Record.Phase {
			case Committed:
				row.state.State = "succeeded"
			case RolledBack:
				row.state.State = "failed"
			default:
				row.state.State = "interrupted"
				if c.sourceAlive != nil && c.sourceAlive(entry.Context.OperationID) {
					row.state.State = "updating"
				}
			}
			if r.Reason != "" {
				reason := r.Reason
				row.state.ErrorCode = &reason
			}
		}
		if at, ok := history.Latest[id]; ok {
			entry := history.Entries[at]
			asset, err := c.store.ReadSourceAsset(entry.Receipt.AssetSHA256)
			if err != nil {
				return bad(config.ErrReadUnavailable)
			}
			row.latest = &sourceListAsset{entry, asset}
			snapshot := entry.Receipt.SnapshotID
			row.state.LatestSnapshot = &snapshot
			row.state.LastSuccess = entry.Receipt.FinishedAt
		}
		for _, file := range bundle.Manifest.Files {
			if file.Kind != "provider" || file.Name != "provider-"+hash([]byte(id))+".json" {
				continue
			}
			for _, entry := range history.Entries {
				if entry.Context.Source.ID == id && entry.Operation.Record.Phase == Committed && entry.Receipt.AssetSHA256 == file.SHA256 {
					row.applied = &sourceListAsset{entry, bundle.Files[file.Name]}
					snapshot := entry.Receipt.SnapshotID
					row.state.AppliedSnapshot = &snapshot
					break
				}
			}
			if row.applied == nil {
				return bad(config.ErrReadUnavailable)
			}
		}
		row.state.Pending = row.state.LatestSnapshot != nil && (row.state.AppliedSnapshot == nil || *row.state.LatestSnapshot != *row.state.AppliedSnapshot)
		rows = append(rows, row)
	}
	return page, rows, settings, nil
}

func ReadSources(ctx context.Context, paths config.Paths, cursor string) (page SourcePage, err error) {
	c, err := readCoordinator(paths)
	if err != nil {
		return page, err
	}
	defer func() { err = errors.Join(err, c.Close()) }()
	return c.Sources(ctx, cursor)
}
