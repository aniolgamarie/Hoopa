package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
)

// RestoreAsset rebuilds private snapshot authority from a v1 SensitiveAssetBytes
// document and explicit trusted parse options. Stored metadata is never trusted:
// the current parser and writer must reproduce the complete input byte for byte.
// Assets contain credentials and must remain in private storage. This function
// performs no IO and does not recover an identity key from the document.
func RestoreAsset(raw []byte, options ParseOptions) (Snapshot, error) {
	// The asset includes base64 original bytes as well as metadata; its envelope
	// has a separate fixed bound from the provider limits supplied to Parse.
	if len(raw) > 64<<20 {
		return Snapshot{}, errors.Join(ErrInvalidProvider, ErrInputLimit)
	}
	var envelope struct {
		Original []byte `json:"original_bytes"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return Snapshot{}, ErrInvalidProvider
	}
	snapshot, err := Parse(envelope.Original, options)
	if err != nil {
		return Snapshot{}, ErrInvalidProvider
	}
	canonical, err := snapshot.SensitiveAssetBytes()
	// Exact comparison also rejects unknown/duplicate/missing fields, nulls,
	// alternate escapes, Unicode replacement and noncanonical representations.
	if err != nil || !bytes.Equal(canonical, raw) {
		return Snapshot{}, ErrInvalidProvider
	}
	return snapshot, nil
}
