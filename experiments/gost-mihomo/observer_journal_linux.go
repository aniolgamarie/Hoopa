//go:build linux

package main

import (
	"encoding/json"
	"errors"

	"hoopa/experiments/telemetry"
)

type linuxObserverJournal struct{ journal *telemetry.Journal }

func openObserverJournal(dir string) (observerJournal, error) {
	j, err := telemetry.OpenJournal(dir)
	if err != nil {
		return nil, err
	}
	return &linuxObserverJournal{j}, nil
}
func (j *linuxObserverJournal) Append(epoch, id string, payload json.RawMessage) (uint64, error) {
	seq, err := j.journal.Append(epoch, id, payload)
	if errors.Is(err, telemetry.ErrConflict) {
		return 0, errObserverJournalConflict
	}
	if errors.Is(err, telemetry.ErrInvalid) {
		return 0, errObserverJournalInvalid
	}
	if err != nil {
		return 0, errObserverJournalUncertain
	}
	return seq, nil
}
func (j *linuxObserverJournal) Replay() []observerJournalRecord {
	raw := j.journal.Replay()
	records := make([]observerJournalRecord, len(raw))
	for i, r := range raw {
		records[i] = observerJournalRecord{r.Seq, r.Epoch, r.ID, r.Payload}
	}
	return records
}
func (j *linuxObserverJournal) Close() error { return j.journal.Close() }
