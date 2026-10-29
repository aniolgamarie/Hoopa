// Package coreadmission is a finite isolated-guest experiment, not a production identity authority.
package coreadmission

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"hoopa/protocol/identityprofile"
	"io"
	"regexp"
	"strconv"
)

var mapNames = []string{"capture_config", "connect_records", "capture_faults", "capture_gate", "online_tuples", "online_bindings"}
var programNames = []string{"hoopa_connect", "hoopa_lifecycle", "hoopa_stop"}
var linkNames = []string{"hoopa_connect", "hoopa_lifecycle"}
var positiveDecimal = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var sourcePath = regexp.MustCompile(`^/sys/fs/cgroup/hoopa-online-[A-Za-z0-9_-]+$`)

type handoff struct {
	IdentityProfile string         `json:"identity_profile,omitempty"`
	Schema          string         `json:"schema"`
	CoreGeneration  string         `json:"core_generation"`
	Generation      string         `json:"generation"`
	SourceCgroupID  string         `json:"source_cgroup_id"`
	RootCgroupID    string         `json:"root_cgroup_id"`
	NetnsCookie     string         `json:"netns_cookie"`
	PIDNSInode      uint32         `json:"pidns_inode"`
	UserNSInode     uint32         `json:"userns_inode,omitempty"`
	MountNSInode    uint32         `json:"mountns_inode,omitempty"`
	BootID          string         `json:"boot_id"`
	SourcePath      string         `json:"source_path"`
	Port            uint16         `json:"port"`
	Maps            map[string]int `json:"maps"`
	Programs        map[string]int `json:"programs"`
	Links           map[string]int `json:"links"`
}

func u64(s string) (uint64, error) {
	if !positiveDecimal.MatchString(s) {
		return 0, errors.New("noncanonical positive uint64")
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// rejectDuplicates runs before typed decoding, including inside all FD objects.
func jsonValue(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate/invalid JSON key")
			}
			seen[name] = true
			if err = jsonValue(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err = jsonValue(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func parse(raw []byte) (handoff, error) {
	var h handoff
	if len(raw) == 0 || len(raw) > 8192 {
		return h, errors.New("handoff must contain 1..8192 bytes")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := jsonValue(d); err != nil {
		return h, err
	}
	if _, err := d.Token(); err != io.EOF {
		return h, errors.New("trailing JSON")
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return h, err
	}
	names := []string{"schema", "core_generation", "generation", "source_cgroup_id", "root_cgroup_id", "netns_cookie", "pidns_inode", "boot_id", "source_path", "port", "maps", "programs", "links"}
	var schema string
	if err := json.Unmarshal(keys["schema"], &schema); err != nil {
		return h, err
	}
	if schema == "hoopa-core-identity-handoff-v3" || schema == identityprofile.HandoffSchema {
		names = append(names, "userns_inode", "mountns_inode")
	}
	if schema == identityprofile.HandoffSchema {
		names = append(names, "identity_profile")
	}
	if len(keys) != len(names) {
		return h, errors.New("unexpected handoff field set")
	}
	for _, name := range names {
		if _, ok := keys[name]; !ok {
			return h, fmt.Errorf("missing exact field %s", name)
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&h); err != nil {
		return h, err
	}
	if (h.Schema != "hoopa-core-identity-handoff-v1" && h.Schema != "hoopa-core-identity-handoff-v2" && h.Schema != "hoopa-core-identity-handoff-v3" && h.Schema != identityprofile.HandoffSchema) || h.CoreGeneration == "" || h.PIDNSInode == 0 || !uuid.MatchString(h.BootID) || !sourcePath.MatchString(h.SourcePath) || h.Port == 0 {
		return h, errors.New("invalid handoff declaration")
	}
	if h.Schema == identityprofile.HandoffSchema && h.IdentityProfile != identityprofile.ID {
		return h, errors.New("invalid identity profile")
	}
	if h.scoped() && (h.UserNSInode == 0 || h.MountNSInode == 0) {
		return h, errors.New("nonzero scoped namespaces required")
	}
	for _, s := range []string{h.Generation, h.SourceCgroupID, h.RootCgroupID, h.NetnsCookie} {
		if _, err := u64(s); err != nil {
			return h, err
		}
	}
	if h.SourceCgroupID == h.RootCgroupID {
		return h, errors.New("source must differ from root")
	}
	seen := map[int]bool{}
	for _, group := range []struct {
		fds   map[string]int
		names []string
	}{{h.Maps, h.mapNames()}, {h.Programs, h.programNames()}, {h.Links, h.linkNames()}} {
		if len(group.fds) != len(group.names) {
			return h, errors.New("unexpected FD object size")
		}
		for _, name := range group.names {
			fd, ok := group.fds[name]
			if !ok || fd < 200 || fd > 1023 || seen[fd] {
				return h, fmt.Errorf("invalid/duplicate FD for %s", name)
			}
			seen[fd] = true
		}
	}
	return h, nil
}

func (h handoff) orderedFDs() []int {
	fds := make([]int, 0, len(h.Maps)+len(h.Programs)+len(h.Links))
	for _, name := range h.mapNames() {
		fds = append(fds, h.Maps[name])
	}
	for _, name := range h.programNames() {
		fds = append(fds, h.Programs[name])
	}
	for _, name := range h.linkNames() {
		fds = append(fds, h.Links[name])
	}
	return fds
}
