//go:build linux

package config

import (
	"errors"
	"os"
)

// ReadLauncherIntent validates the inherited private directory and the exact
// intent digest sent by the manager. This does not grant another spawn ticket.
func ReadLauncherIntent(dir *os.Root, expectedSHA string) (RunIntent, error) {
	if dir == nil || !isDigest(expectedSHA) {
		return RunIntent{}, ErrRunConflict
	}
	info, err := dir.Stat(".")
	if err != nil {
		return RunIntent{}, err
	}
	if err = privateInfo(info, true); err != nil {
		return RunIntent{}, err
	}
	data, err := readBounded(dir, "intent.json", MaxRunRecordBytes)
	if err != nil {
		return RunIntent{}, err
	}
	var intent RunIntent
	if err = decode(data, &intent); err != nil {
		return RunIntent{}, err
	}
	if err = intent.Validate(); err != nil {
		return RunIntent{}, err
	}
	if digest(data) != expectedSHA {
		return RunIntent{}, ErrRunConflict
	}
	return intent, nil
}

// ClaimRunBirth is the launcher's sole delegated write, not a manager mutation.
// The launcher supplies its just-observed boot/start, publishes before exec,
// and must abort exec on ANY error. This claim alone is never core ownership:
// the manager later compares its contents with the actual core via a pidfd.
// dir must come from PreparedRun.TakeBirthDirectory, inherited by that launcher.
func ClaimRunBirth(dir *os.Root, expectedSHA, nonce string, birth RunBirth) error {
	return claimRunBirth(dir, expectedSHA, nonce, birth, &Store{})
}

func claimRunBirth(dir *os.Root, expectedSHA, nonce string, birth RunBirth, writes *Store) error {
	intent, err := ReadLauncherIntent(dir, expectedSHA)
	if err != nil {
		return err
	}
	if !isDigest(nonce) || digest([]byte(nonce)) != intent.NonceSHA256 || birth.PID != os.Getpid() || birth.UID != uint32(os.Geteuid()) {
		return ErrRunConflict
	}
	if err = birth.Match(intent, expectedSHA); err != nil {
		return err
	}
	// Never treat an identical prior claim as an idempotent right to exec again.
	if _, err = dir.Lstat("birth.json"); !errors.Is(err, os.ErrNotExist) {
		return errors.Join(ErrRunConflict, err)
	}
	data, err := encode(birth)
	if err != nil || len(data) > MaxRunRecordBytes {
		return errors.Join(ErrIntegrity, err)
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	tmp, linked := ".birth-"+token, false
	defer func() {
		if !linked {
			dir.Remove(tmp)
		}
	}()
	// writeNew verifies Write/Sync/Close before any published hard link exists.
	if err = writes.writeNew(dir, tmp, data, "birth.file"); err != nil {
		return err
	}
	if err = writes.checkpoint("birth.link"); err != nil {
		return err
	}
	if err = dir.Link(tmp, "birth.json"); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.Join(ErrRunConflict, err)
		}
		return writes.uncertain(err)
	}
	linked = true
	if err = writes.checkpoint("birth.unlink-temp"); err != nil {
		return writes.uncertain(err)
	}
	if err = dir.Remove(tmp); err != nil {
		return writes.uncertain(err)
	}
	if err = writes.syncDir(dir, "birth.parent-sync"); err != nil {
		return writes.uncertain(err)
	}
	// The final record must have exactly one link; do not pass a writable alias
	// or an unfinished publication to exec, even inside the same-UID boundary.
	stored, sha, err := readRunBirth(dir, intent, expectedSHA)
	if err != nil || stored != birth || sha != digest(data) {
		return errors.Join(ErrRunConflict, err)
	}
	return nil
}
