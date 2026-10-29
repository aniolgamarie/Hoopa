package mihomoevents

import (
	"crypto/sha256"
	"encoding/hex"
	"io"

	"hoopa/protocol/telemetrywire"
)

// ReadSourceRecovery inspects an explicitly supplied frozen manifest/frame
// pair. It does not acquire a writer lock, Sync, create files, publish a receipt
// or claim an epoch ended. Missing finals therefore remain pending here.
func ReadSourceRecovery(reader io.Reader, rawManifest []byte) (telemetrywire.RecoveryReport, error) {
	m, err := telemetrywire.DecodeManifest(rawManifest)
	if err != nil {
		return telemetrywire.RecoveryReport{}, ErrInvalidSourceLifecycle
	}
	h := sha256.New()
	// The manifest budget covers the pair, not just each individually valid
	// frame. Read at most that remainder plus one overflow byte; a hostile or
	// accidentally unbounded reader cannot turn a snapshot into unlimited work.
	bounded := &io.LimitedReader{R: reader, N: int64(m.MaxSourceBytes) - int64(len(rawManifest)) + 1}
	life, err := readWireLifecycle(io.TeeReader(bounded, h), m.SourceEpoch, m)
	if err != nil || bounded.N == 0 {
		return telemetrywire.RecoveryReport{}, ErrInvalidSourceLifecycle
	}
	report, err := telemetrywire.ProjectRecovery(m, life, hex.EncodeToString(h.Sum(nil)), nil)
	if err != nil {
		return telemetrywire.RecoveryReport{}, ErrInvalidSourceLifecycle
	}
	return report, nil
}
