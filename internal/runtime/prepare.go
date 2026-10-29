//go:build linux && amd64

package runtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"hoopa/internal/config"
)

// PreparedLaunch contains a durable one-use launch ticket and its private nonce.
// It neither starts a process nor changes the caller's environment.
type PreparedLaunch struct {
	intent config.RunIntent
	ticket *config.PreparedRun
	nonce  string
}

// String and GoString deliberately suppress all launch material, including
// private fields that fmt would otherwise expose with %#v.
func (PreparedLaunch) String() string   { return "PreparedLaunch{redacted}" }
func (PreparedLaunch) GoString() string { return "PreparedLaunch{redacted}" }

// Intent returns a detached snapshot. The raw nonce is never part of the intent.
func (p *PreparedLaunch) Intent() config.RunIntent {
	if p == nil {
		return config.RunIntent{}
	}
	intent := p.intent
	intent.Arguments = append([]string(nil), intent.Arguments...)
	return intent
}

// TakeLauncherInput transfers the one-use birth-directory capability first.
// Any failure returns neither the raw nonce nor an intent digest.
func (p *PreparedLaunch) TakeLauncherInput() (*os.File, string, string, error) {
	if p == nil || p.ticket == nil {
		return nil, "", "", config.ErrRunConflict
	}
	directory, err := p.ticket.TakeBirthDirectory()
	if err != nil {
		return nil, "", "", err
	}
	return directory, p.ticket.Digest(), p.nonce, nil
}

// PrepareLaunch creates independent CSPRNG run/nonce material. A supplied run or
// nonce digest is rejected rather than treated as a reusable spawn permission.
func PrepareLaunch(store *config.Store, input config.RunIntent) (*PreparedLaunch, error) {
	if store == nil || input.RunID != "" || input.NonceSHA256 != "" {
		return nil, config.ErrIntegrity
	}
	input.Arguments = append([]string(nil), input.Arguments...)
	var nonceBytes [32]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		return nil, errors.Join(config.ErrIntegrity, err)
	}
	var runBytes [16]byte
	if _, err := rand.Read(runBytes[:]); err != nil {
		return nil, errors.Join(config.ErrIntegrity, err)
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	input.RunID = config.RunID("run:" + hex.EncodeToString(runBytes[:]))
	sum := sha256.Sum256([]byte(nonce))
	input.NonceSHA256 = hex.EncodeToString(sum[:])
	ticket, err := store.PrepareRun(input)
	if err != nil {
		return nil, err
	}
	return &PreparedLaunch{intent: input, ticket: ticket, nonce: nonce}, nil
}
