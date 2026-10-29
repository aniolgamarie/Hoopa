// sourceinspect reads an explicitly selected, frozen P0 source snapshot. It
// writes nothing, sends no ACK, and does not qualify any product capability.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"

	events "hoopa/experiments/mihomo-events"
)

func main() {
	path := flag.String("frames", "", "frozen source-frame file")
	epoch := flag.String("epoch", "", "expected source epoch")
	manifest := flag.String("manifest", "", "optional paired frozen manifest; missing finals remain pending")
	flag.Parse()
	if *path == "" || (*epoch == "" && *manifest == "") || flag.NArg() != 0 {
		os.Exit(2)
	}
	info, err := os.Lstat(*path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		fail()
	}
	f, err := os.Open(*path)
	if err != nil {
		fail()
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		fail()
	}
	if *manifest != "" {
		meta, err := openSnapshot(*manifest)
		if err != nil {
			fail()
		}
		metaBefore, err := meta.Stat()
		if err != nil {
			_ = meta.Close()
			fail()
		}
		raw, err := io.ReadAll(io.LimitReader(meta, 8193))
		stable := unchanged(meta, metaBefore)
		_ = meta.Close()
		if err != nil || !stable {
			fail()
		}
		report, err := events.ReadSourceRecovery(f, raw)
		if err != nil || (*epoch != "" && report.SourceEpoch != *epoch) || !unchanged(f, opened) {
			fail()
		}
		if json.NewEncoder(os.Stdout).Encode(report) != nil {
			os.Exit(1)
		}
		return
	}
	h := sha256.New()
	report, err := events.ReadSourceLifecycle(io.TeeReader(f, h), *epoch)
	if err != nil || !unchanged(f, opened) {
		fail()
	}
	counts := map[string]int{"pending": 0, "gap": 0, "closed": 0}
	for _, flow := range report.Flows {
		counts[flow.State]++
	}
	result := struct {
		Scope          string         `json:"scope"`
		Valid          bool           `json:"snapshot_valid"`
		Digest         string         `json:"snapshot_sha256"`
		EpochFinality  string         `json:"epoch_finality"`
		LastSourceSeq  uint64         `json:"last_source_seq,string"`
		Flows          map[string]int `json:"flows"`
		DoesNotPassG02 bool           `json:"does_not_pass_g02"`
	}{"T133_read_only_source_snapshot", true, hex.EncodeToString(h.Sum(nil)), report.EpochFinality, report.LastSourceSeq, counts, true}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(1)
	}
}

func openSnapshot(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, os.ErrPermission
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, os.ErrPermission
	}
	return f, nil
}

func unchanged(f *os.File, before os.FileInfo) bool {
	after, err := f.Stat()
	return err == nil && os.SameFile(before, after) && before.Size() == after.Size() && before.ModTime() == after.ModTime()
}

func fail() {
	_, _ = os.Stderr.WriteString("source snapshot invalid or unavailable\n")
	os.Exit(1)
}
