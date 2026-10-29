package coreadmission

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"hoopa/protocol/identityprofile"
	"syscall"
)

var scopedMapNames = append(append([]string{}, executableMapNames...), "identity_scope")

func (h handoff) scoped() bool {
	return h.Schema == "hoopa-core-identity-handoff-v3" || h.Schema == identityprofile.HandoffSchema
}

func identityScopeConfig(h handoff) []byte {
	raw := make([]byte, 24)
	generation, _ := u64(h.Generation)
	le.PutUint64(raw, generation)
	le.PutUint32(raw[8:], h.UserNSInode)
	le.PutUint32(raw[12:], h.MountNSInode)
	le.PutUint32(raw[16:], 1)
	return raw
}

// checkConfig calls this after the unchanged capture_config check and before
// gate/fault reads. Legacy schemas perform no additional lookup.
func (o *Owner) checkIdentityScope() error {
	if !o.h.scoped() {
		return nil
	}
	raw, err := o.lookup("identity_scope", make([]byte, 4), 0)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, identityScopeConfig(o.h)) {
		return errors.New("identity scope readback mismatch")
	}
	o.scopeRawHex = hex.EncodeToString(raw)
	return nil
}
func (o *Owner) freezeIdentityScope() error {
	if !o.h.scoped() {
		return nil
	}
	if err := o.b.update(o.h.Maps["identity_scope"], make([]byte, 4), identityScopeConfig(o.h)); !errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("identity scope freeze probe must be EPERM, got %v", err)
	}
	return o.checkIdentityScope()
}

// namespaceMatches preserves the legacy checks and binds v3's actual user/mnt
// coordinates to both the immutable handoff and the current PID1 namespace.
func (h handoff) namespaceMatches(name string, self, initial uint64) bool {
	if self != initial || name == "pid" && self != uint64(h.PIDNSInode) {
		return false
	}
	if h.scoped() {
		if name == "user" {
			return self == uint64(h.UserNSInode)
		}
		if name == "mnt" {
			return self == uint64(h.MountNSInode)
		}
	}
	return true
}

// FieldProfile returns values only after successful Adopt/readback. It does not
// read descriptors or turn a legacy handoff into a field grant.
func (o *Owner) FieldProfile() (identityprofile.Profile, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.status.Revoked || o.h.Schema != identityprofile.HandoffSchema {
		return identityprofile.Profile{}, identityprofile.ErrProfile
	}
	return identityprofile.New(o.scopeRawHex, o.h.Generation)
}
