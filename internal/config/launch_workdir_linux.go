//go:build linux

package config

import "os"

// OpenRunWorkdir verifies the existing private basic-core working directory and
// config.json original bytes, then returns an inode-anchored caller-owned root.
// It never creates a directory/config, repairs permissions or grants a launch.
func OpenRunWorkdir(intent RunIntent) (*os.Root, error) {
	if intent.Validate() != nil {
		return nil, ErrIntegrity
	}
	root, err := privateRoot(intent.Workdir, false)
	if err != nil {
		return nil, err
	}
	raw, err := readBounded(root, "config.json", 65536)
	if err != nil || len(raw) == 0 || digest(raw) != intent.ConfigSHA256 {
		root.Close()
		return nil, ErrIntegrity
	}
	return root, nil
}
