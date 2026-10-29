package mihomo

import "hoopa/internal/config"

// ApprovedBasicCapability binds a local installation path to the reviewed
// finite basic profile. It does not install a core or trust its own API claim.
// Start and runtime ownership still verify the actual binary and process.
// The evidence artifact is experiments/mihomo-events/approved-basic-core-v1.json;
// changing the core or expanding capabilities requires a new reviewed artifact.
func ApprovedBasicCapability(binaryPath string) (CapabilityRecord, error) {
	c := CapabilityRecord{
		SchemaVersion: 1,
		Binary: config.BinaryReference{
			Path: binaryPath, Version: "1.10.0",
			SHA256: "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb",
		},
		GOOS: "linux", GOARCH: "amd64", Protocol: 1, Mode: "basic",
		Capabilities:   []string{"authenticated-activate-v1", "closed-start-v1", "immutable-snapshot-v1"},
		EvidenceSHA256: "50b3ba6bbd309e3273784695001c15bfaf2d4c1d5b8709421357fae249f7df77",
		Accepted:       true,
	}
	if err := c.Validate(); err != nil {
		return CapabilityRecord{}, err
	}
	return c, nil
}

// ApprovedSourcesCapability is the separately reviewed finite sources-v2 core.
// Its evidence does not extend the original-process/telemetry capability set.
func ApprovedSourcesCapability(binaryPath string) (CapabilityRecord, error) {
	c := CapabilityRecord{
		SchemaVersion: 1,
		Binary:        config.BinaryReference{Path: binaryPath, Version: "1.10.0", SHA256: "c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6"},
		GOOS:          "linux", GOARCH: "amd64", Protocol: 2, Mode: "basic-sources",
		Capabilities:   []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "probe-qualified-groups-v1"},
		EvidenceSHA256: "e61272a2d656e08249ac0067ea83d1489f4e98a01fee7f88947fd63f039f2bbc", Accepted: true,
	}
	if err := c.Validate(); err != nil {
		return CapabilityRecord{}, err
	}
	return c, nil
}

// ApprovedAppRoutingCapability is a separate bounded qualification. Its v2
// wire profile does not promote the original source core or grant process facts.
// Actual installed bytes and original process still require pinned-FD checks.
func ApprovedAppRoutingCapability(binaryPath string) (CapabilityRecord, error) {
	c := CapabilityRecord{
		SchemaVersion: 1,
		Binary:        config.BinaryReference{Path: binaryPath, Version: "1.10.0", SHA256: "6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b"},
		GOOS:          "linux", GOARCH: "amd64", Protocol: 2, Mode: "basic-app-routing",
		Capabilities:   []string{"authenticated-activate-v1", "closed-start-v1", "immutable-source-snapshot-v2", "ordered-app-rules-v1", "probe-qualified-groups-v1", "unknown-ip-reject-v1"},
		EvidenceSHA256: "06724bac18f5557552e983ee48cce9e3564f3fd222c4ad3e8fc0d018324146e5", Accepted: true,
	}
	if err := c.Validate(); err != nil {
		return CapabilityRecord{}, err
	}
	return c, nil
}

// ApprovedCapability selects from fixed local evidence using the COMPLETE
// immutable binary reference. A supplied protocol/mode or core response cannot
// choose a profile. No filesystem access or installation happens here.
func ApprovedCapability(binary config.BinaryReference) (CapabilityRecord, error) {
	var c CapabilityRecord
	var err error
	switch binary.SHA256 {
	case "2921c2468cf1e62099ac40a1872fbb58cc0048696a91c9dd920dc9790f27fabb":
		c, err = ApprovedBasicCapability(binary.Path)
	case "c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6":
		c, err = ApprovedSourcesCapability(binary.Path)
	case "6ec6a329801a49e69ead6c093955a07c4d1098bdfb26b0b611eae0b5205f745b":
		c, err = ApprovedAppRoutingCapability(binary.Path)
	default:
		return CapabilityRecord{}, ErrCapability
	}
	if err != nil || c.Binary != binary {
		return CapabilityRecord{}, ErrCapability
	}
	return c, nil
}
