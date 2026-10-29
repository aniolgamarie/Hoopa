//go:build linux

package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// RunIntent binds one launch ticket to immutable configuration. Validate checks
// shape only; bundle membership and durable publication belong to the store.
type RunIntent struct {
	Version        int             `json:"version"`
	RunID          RunID           `json:"run_id"`
	OperationID    ID              `json:"operation_id"`
	Generation     Generation      `json:"generation"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	Binary         BinaryReference `json:"binary"`
	Workdir        string          `json:"workdir"`
	Arguments      []string        `json:"arguments"`
	Controller     string          `json:"controller"`
	ConfigSHA256   string          `json:"config_sha256"`
	NonceSHA256    string          `json:"nonce_sha256"`
}

// RunBirth records the launcher's pre-exec identity, not process-control rights.
type RunBirth struct {
	Version      int    `json:"version"`
	RunID        RunID  `json:"run_id"`
	IntentSHA256 string `json:"intent_sha256"`
	PID          int    `json:"pid"`
	UID          uint32 `json:"uid"`
	BootID       string `json:"boot_id"`
	StartTicks   uint64 `json:"start_ticks,string"`
}

// RunRegistration preserves a fresh ownership verification for this operation.
// Reading or matching a record does not repeat that verification or grant control.
type RunRegistration struct {
	Version        int        `json:"version"`
	RunID          RunID      `json:"run_id"`
	OperationID    ID         `json:"operation_id"`
	IntentSHA256   string     `json:"intent_sha256"`
	BirthSHA256    string     `json:"birth_sha256"`
	PID            int        `json:"pid"`
	UID            uint32     `json:"uid"`
	BootID         string     `json:"boot_id"`
	StartTicks     uint64     `json:"start_ticks,string"`
	Generation     Generation `json:"generation"`
	ManifestSHA256 string     `json:"manifest_sha256"`
	BinarySHA256   string     `json:"binary_sha256"`
	ConfigSHA256   string     `json:"config_sha256"`
	NonceSHA256    string     `json:"nonce_sha256"`
	Controller     string     `json:"controller"`
	Gate           string     `json:"gate"`
}

func runIDs(values ...string) bool {
	for _, value := range values {
		if _, err := ParseID(value); err != nil {
			return false
		}
	}
	return true
}
func runDigests(values ...string) bool {
	for _, value := range values {
		if !isDigest(value) {
			return false
		}
	}
	return true
}
func runPath(path string) bool {
	return len(path) <= 4096 && !strings.ContainsRune(path, 0) && filepath.IsAbs(path) && filepath.Clean(path) == path
}
func runController(controller string) bool {
	const prefix = "127.0.0.1:"
	if !strings.HasPrefix(controller, prefix) {
		return false
	}
	port, err := strconv.Atoi(strings.TrimPrefix(controller, prefix))
	return err == nil && port >= 1 && port <= 65535 && controller == prefix+strconv.Itoa(port)
}
func runIdentity(pid int, boot string, ticks uint64) bool {
	if pid < 1 || int64(pid) > 2147483647 || ticks == 0 || len(boot) != 36 {
		return false
	}
	for i := 0; i < len(boot); i++ {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if boot[i] != '-' {
				return false
			}
			continue
		}
		if !(boot[i] >= '0' && boot[i] <= '9' || boot[i] >= 'a' && boot[i] <= 'f') {
			return false
		}
	}
	return true
}

func (r RunIntent) Validate() error {
	if r.Version != 1 || !runIDs(string(r.RunID), string(r.OperationID), string(r.Generation)) ||
		!runDigests(r.ManifestSHA256, r.Binary.SHA256, r.ConfigSHA256, r.NonceSHA256) ||
		!runPath(r.Binary.Path) || r.Binary.Version == "" || len(r.Binary.Version) > 128 ||
		!runPath(r.Workdir) || !runController(r.Controller) || len(r.Arguments) < 1 || len(r.Arguments) > 64 {
		return fmt.Errorf("%w: invalid run intent", ErrIntegrity)
	}
	if r.Arguments[0] != r.Binary.Path {
		return fmt.Errorf("%w: argv does not start with binary path", ErrIntegrity)
	}
	total := 0
	for _, arg := range r.Arguments {
		if len(arg) > 4096 || strings.ContainsRune(arg, 0) {
			return fmt.Errorf("%w: invalid run argument", ErrIntegrity)
		}
		total += len(arg)
	}
	if total > 32*1024 {
		return fmt.Errorf("%w: run arguments exceed bound", ErrIntegrity)
	}
	return nil
}
func (r RunBirth) Validate() error {
	if r.Version != 1 || !runIDs(string(r.RunID)) || !isDigest(r.IntentSHA256) || !runIdentity(r.PID, r.BootID, r.StartTicks) {
		return fmt.Errorf("%w: invalid run birth", ErrIntegrity)
	}
	return nil
}
func (r RunRegistration) Validate() error {
	if r.Version != 1 || !runIDs(string(r.RunID), string(r.OperationID), string(r.Generation)) ||
		!runDigests(r.IntentSHA256, r.BirthSHA256, r.ManifestSHA256, r.BinarySHA256, r.ConfigSHA256, r.NonceSHA256) ||
		!runIdentity(r.PID, r.BootID, r.StartTicks) || !runController(r.Controller) || (r.Gate != "closed" && r.Gate != "open") {
		return fmt.Errorf("%w: invalid run registration", ErrIntegrity)
	}
	return nil
}

// Match expects digests of the protected original bytes, supplied by the caller.
// It does not serialize, hash, or perform IO on those originals.
func (r RunBirth) Match(intent RunIntent, intentSHA string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := intent.Validate(); err != nil {
		return err
	}
	if !isDigest(intentSHA) || r.IntentSHA256 != intentSHA || r.RunID != intent.RunID {
		return fmt.Errorf("%w: birth/intent binding mismatch", ErrIntegrity)
	}
	return nil
}
func (r RunRegistration) Match(intent RunIntent, intentSHA string, birth RunBirth, birthSHA string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := birth.Match(intent, intentSHA); err != nil {
		return err
	}
	if !isDigest(birthSHA) || r.RunID != intent.RunID || r.IntentSHA256 != intentSHA || r.BirthSHA256 != birthSHA ||
		r.PID != birth.PID || r.UID != birth.UID || r.BootID != birth.BootID || r.StartTicks != birth.StartTicks ||
		r.Generation != intent.Generation || r.ManifestSHA256 != intent.ManifestSHA256 || r.BinarySHA256 != intent.Binary.SHA256 ||
		r.ConfigSHA256 != intent.ConfigSHA256 || r.NonceSHA256 != intent.NonceSHA256 || r.Controller != intent.Controller {
		return fmt.Errorf("%w: registration binding mismatch", ErrIntegrity)
	}
	return nil
}
