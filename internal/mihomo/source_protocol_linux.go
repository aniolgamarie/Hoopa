//go:build linux && amd64

package mihomo

import (
	"encoding/json"

	wire "hoopa/protocol/corecontrol"
	sources "hoopa/protocol/sourcecontrol"
)

// The capability, bound to an exact binary by bundle admission, chooses the
// protocol. Neither response mode nor a received schema version chooses it.
func (c *Controller) controlPrefix() string {
	if c.capabilities.Protocol == 2 {
		return "/hoopa/v2/"
	}
	return "/hoopa/v1/"
}

func decodeControllerSnapshot(protocol int, raw []byte) (canonical []byte, configSHA, controller, digest string, err error) {
	switch protocol {
	case 1:
		s, e := wire.DecodeSnapshot(raw)
		if e != nil {
			return nil, "", "", "", ErrController
		}
		canonical, e = json.Marshal(s)
		if e != nil {
			return nil, "", "", "", ErrController
		}
		digest, e = s.Digest()
		return canonical, s.ConfigSHA256, s.Controller, digest, e
	case 2:
		s, e := sources.DecodeSnapshot(raw)
		if e != nil {
			return nil, "", "", "", ErrController
		}
		canonical, e = json.Marshal(s)
		if e != nil {
			return nil, "", "", "", ErrController
		}
		digest, e = s.Digest()
		return canonical, s.ConfigSHA256, s.Controller, digest, e
	default:
		return nil, "", "", "", ErrController
	}
}

// Status/Result retain the existing Go API shape as a common record. A v2
// record keeps schema_version=2 and is validated by sourcecontrol BEFORE this
// conversion; callers must not revalidate it using the v1 wire methods.
func decodeControllerStatus(protocol int, raw []byte) (wire.Status, error) {
	if protocol == 1 {
		return wire.DecodeStatus(raw)
	}
	if protocol != 2 {
		return wire.Status{}, ErrController
	}
	s, e := sources.DecodeStatus(raw)
	if e != nil {
		return wire.Status{}, ErrController
	}
	v := wire.Status{SchemaVersion: s.SchemaVersion, RunID: s.RunID, ConfigGeneration: s.ConfigGeneration, ConfigSHA256: s.ConfigSHA256, BinaryVersion: s.BinaryVersion, Mode: s.Mode, Capabilities: append([]string(nil), s.Capabilities...), State: s.State, SnapshotSHA256: s.SnapshotSHA256}
	if s.Activation != nil {
		x := wire.Result(*s.Activation)
		v.Activation = &x
	}
	if s.Drain != nil {
		x := wire.Result(*s.Drain)
		v.Drain = &x
	}
	return v, nil
}
func controllerMutationDigest(protocol int, q wire.Mutation) (string, error) {
	if protocol == 1 {
		return q.Digest()
	}
	if protocol == 2 {
		return sources.Mutation(q).Digest()
	}
	return "", ErrController
}
func decodeControllerResult(protocol int, raw []byte) (wire.Result, error) {
	if protocol == 1 {
		return wire.DecodeResult(raw)
	}
	if protocol == 2 {
		r, e := sources.DecodeResult(raw)
		return wire.Result(r), e
	}
	return wire.Result{}, ErrController
}
