//go:build linux

package telemetry

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func openTestJournal(t *testing.T) (*Journal, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.Close() })
	return j, dir
}
func TestJournalReplayDedupConflictAndExactPayload(t *testing.T) {
	j, dir := openTestJournal(t)
	payload := json.RawMessage(" \n{\"large\":9007199254740993,\"max\":18446744073709551615,\"bytes\":\"18446744073709551615\"} \t")
	seq, err := j.Append("epoch-1", "sample-1", payload)
	if err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
	file := filepath.Join(dir, dataName)
	before, _ := os.ReadFile(file)
	seq, err = j.Append("epoch-1", "sample-1", payload)
	if err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(before, after) {
		t.Fatal("duplicate wrote another frame")
	}
	if _, err = j.Append("epoch-1", "sample-1", json.RawMessage(`{}`)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if seq, err = j.Append("epoch-2", "sample-1", json.RawMessage(`{"next":true}`)); err != nil || seq != 2 {
		t.Fatal(seq, err)
	}
	payload[0] = 'x'
	r := j.Replay()
	r[0].Payload[0] = 'y'
	if bytes.Equal(j.Replay()[0].Payload, r[0].Payload) {
		t.Fatal("Replay aliases internal bytes")
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	records := j.Replay()
	if len(records) != 2 || records[0].Seq != 1 || records[1].Seq != 2 || records[0].Payload[0] != ' ' || !bytes.Contains(records[0].Payload, []byte("9007199254740993")) || records[0].Payload[len(records[0].Payload)-1] != '\t' {
		t.Fatalf("not byte-exact replay: %+v", records)
	}
	if seq, err = j.Append("epoch-1", "sample-1", records[0].Payload); err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
}
func TestJournalWriterLockAndValidation(t *testing.T) {
	j, dir := openTestJournal(t)
	if _, err := OpenJournal(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second writer: %v", err)
	}
	for _, tc := range []struct {
		epoch, id string
		p         json.RawMessage
	}{{"", "id", json.RawMessage(`{}`)}, {"epoch", "bad/id", json.RawMessage(`{}`)}, {"epoch", "id", json.RawMessage(`[]`)}, {"epoch", "id", json.RawMessage(`null`)}, {"epoch", "id", json.RawMessage(`{} {}`)}, {"epoch", "id", json.RawMessage(`{"n":NaN}`)}, {"epoch", "id", bytes.Repeat([]byte(" "), payloadLimit+1)}} {
		if _, err := j.Append(tc.epoch, tc.id, tc.p); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid accepted: %v", err)
		}
	}
	if len(j.Replay()) != 0 {
		t.Fatal("invalid append changed records")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j2, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	if _, err := j.Append("e", "i", json.RawMessage(`{}`)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	for _, name := range []string{lockName, dataName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal(name, info, err)
		}
	}
}

type injectedFile struct {
	file     journalFile
	short    bool
	writeErr bool
	syncErr  bool
}

func (f injectedFile) Write(p []byte) (int, error) {
	if f.writeErr {
		return 0, errors.New("injected write failure")
	}
	if f.short {
		return f.file.Write(p[:len(p)/2])
	}
	return f.file.Write(p)
}
func (f injectedFile) Sync() error {
	if err := f.file.Sync(); err != nil {
		return err
	}
	if f.syncErr {
		return errors.New("injected sync uncertainty")
	}
	return nil
}
func (f injectedFile) Close() error { return f.file.Close() }
func TestJournalSyncUncertaintyReopenDisambiguates(t *testing.T) {
	j, dir := openTestJournal(t)
	j.file = injectedFile{file: j.file, syncErr: true}
	p := json.RawMessage(`{"bytes":"9007199254740993"}`)
	if seq, err := j.Append("epoch", "sample", p); !errors.Is(err, ErrUncertain) || seq != 0 {
		t.Fatal(seq, err)
	}
	if len(j.Replay()) != 0 {
		t.Fatal("unconfirmed event exposed")
	}
	before, _ := os.ReadFile(filepath.Join(dir, dataName))
	if _, err := j.Append("epoch", "sample", p); !errors.Is(err, ErrUncertain) {
		t.Fatal("poisoned writer retried", err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, dataName))
	if !bytes.Equal(before, after) {
		t.Fatal("poisoned append wrote")
	}
	j.Close()
	j2, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	if seq, err := j2.Append("epoch", "sample", p); err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
	if len(j2.Replay()) != 1 {
		t.Fatal("uncertain event re-counted")
	}
}
func TestJournalShortWriteRequiresRecovery(t *testing.T) {
	j, dir := openTestJournal(t)
	j.file = injectedFile{file: j.file, short: true}
	if _, err := j.Append("epoch", "sample", json.RawMessage(`{}`)); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	j.Close()
	before, _ := os.ReadFile(filepath.Join(dir, dataName))
	if _, err := OpenJournal(dir); !errors.Is(err, ErrRecoveryRequired) {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(dir, dataName))
	if !bytes.Equal(before, after) {
		t.Fatal("tail modified")
	}
}
func TestJournalCorruptFramesRemainUntouched(t *testing.T) {
	j, dir := openTestJournal(t)
	j.Append("epoch", "one", json.RawMessage(`{}`))
	j.Close()
	valid, _ := os.ReadFile(filepath.Join(dir, dataName))
	gap, _ := encodeFrame(Record{Seq: 2, Epoch: "epoch", ID: "one", Payload: json.RawMessage(`{}`)})
	duplicate, _ := encodeFrame(Record{Seq: 2, Epoch: "epoch", ID: "one", Payload: json.RawMessage(`{"changed":true}`)})
	invalidID, _ := encodeFrame(Record{Seq: 1, Epoch: "bad/epoch", ID: "one", Payload: json.RawMessage(`{}`)})
	bad := append([]byte(nil), valid...)
	bad[len(bad)-1] ^= 1
	huge := make([]byte, 4)
	binary.BigEndian.PutUint32(huge, frameLimit+1)
	for name, data := range map[string][]byte{"header": valid[:2], "half": valid[:len(valid)/2], "checksum": bad, "gap": gap, "duplicate": append(append([]byte(nil), valid...), duplicate...), "invalid_id": invalidID, "oversize": huge} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, dataName), data, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenJournal(dir); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, dataName))
			if !bytes.Equal(data, after) {
				t.Fatal("damaged file changed")
			}
		})
	}
}
func TestJournalRejectsUnsafeFilesAndDirectory(t *testing.T) {
	for _, name := range []string{lockName, dataName} {
		t.Run(name, func(t *testing.T) {
			for _, kind := range []string{"symlink", "mode", "directory"} {
				t.Run(kind, func(t *testing.T) {
					dir := t.TempDir()
					os.Chmod(dir, 0700)
					path := filepath.Join(dir, name)
					switch kind {
					case "symlink":
						target := filepath.Join(t.TempDir(), "target")
						os.WriteFile(target, []byte("untouched"), 0600)
						os.Symlink(target, path)
					case "mode":
						os.WriteFile(path, nil, 0644)
					case "directory":
						os.Mkdir(path, 0700)
					}
					if j, err := OpenJournal(dir); !errors.Is(err, ErrUnsafe) {
						if j != nil {
							j.Close()
						}
						t.Fatal("unsafe accepted", err)
					}
				})
			}
		})
	}
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	if _, err := OpenJournal(dir); !errors.Is(err, ErrUnsafe) {
		t.Fatal(err)
	}
}

func TestJournalConcurrentDuplicateIsSingleRecord(t *testing.T) {
	j, _ := openTestJournal(t)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seq, err := j.Append("epoch", "shared-id", json.RawMessage(`{"bytes":"1"}`))
			if err != nil || seq != 1 {
				t.Errorf("concurrent append: %d %v", seq, err)
			}
			_ = j.Replay()
		}()
	}
	wg.Wait()
	if len(j.Replay()) != 1 {
		t.Fatal("concurrent duplicate re-counted")
	}
}
func TestJournalPayloadAndEnvelopeBounds(t *testing.T) {
	j, dir := openTestJournal(t)
	p := json.RawMessage(`{"pad":"` + strings.Repeat("a", payloadLimit-len(`{"pad":""}`)) + `"}`)
	if len(p) != payloadLimit {
		t.Fatal(len(p))
	}
	if _, err := j.Append("epoch", "max", p); err != nil {
		t.Fatal("64KiB valid payload rejected", err)
	}
	j.Close()
	j2, err := OpenJournal(dir)
	if err != nil {
		t.Fatal("base64 envelope exceeded bound", err)
	}
	if len(j2.Replay()[0].Payload) != payloadLimit {
		t.Fatal("payload changed")
	}
	j2.Close()
	oversized, _ := encodeFrame(Record{1, "epoch", "large", append(append(json.RawMessage(nil), p...), ' ')})
	wrap := func(body string) []byte {
		data := []byte(body)
		frame := make([]byte, 4+len(data)+sha256.Size)
		binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
		copy(frame[4:], data)
		sum := sha256.Sum256(data)
		copy(frame[4+len(data):], sum[:])
		return frame
	}
	for name, data := range map[string][]byte{
		"decoded_size":       oversized,
		"version":            wrap(`{"format_version":2,"seq":1,"epoch":"epoch","id":"id","payload":"e30="}`),
		"duplicate_json_key": wrap(`{"format_version":1,"seq":1,"seq":2,"epoch":"epoch","id":"id","payload":"e30="}`),
		"invalid_base64":     wrap(`{"format_version":1,"seq":1,"epoch":"epoch","id":"id","payload":"!"}`),
	} {
		t.Run(name, func(t *testing.T) {
			os.WriteFile(filepath.Join(dir, dataName), data, 0600)
			if _, err := OpenJournal(dir); !errors.Is(err, ErrRecoveryRequired) {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(dir, dataName))
			if !bytes.Equal(data, after) {
				t.Fatal("recovery modified file")
			}
		})
	}
}

func TestJournalWriteErrorPoisonsUntilReopen(t *testing.T) {
	j, dir := openTestJournal(t)
	j.file = injectedFile{file: j.file, writeErr: true}
	if _, err := j.Append("epoch", "sample", json.RawMessage(`{}`)); !errors.Is(err, ErrUncertain) {
		t.Fatal(err)
	}
	if _, err := j.Append("epoch", "other", json.RawMessage(`{}`)); !errors.Is(err, ErrUncertain) {
		t.Fatal("writer not poisoned", err)
	}
	if len(j.Replay()) != 0 {
		t.Fatal("failed append exposed")
	}
	j.Close()
	j2, err := OpenJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j2.Close()
	if seq, err := j2.Append("epoch", "sample", json.RawMessage(`{}`)); err != nil || seq != 1 {
		t.Fatal(seq, err)
	}
}
