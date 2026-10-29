//go:build linux

package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hoopa/internal/p1testhook"
	"os"
)

// ErrUncertain requires closing this writer and reconciling persisted state.
// A rename followed by failed directory sync cannot be reported as a rollback.
var ErrUncertain = errors.New("STORE_WRITE_UNCERTAIN")

func randomToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// checkpoint is a package-private seam for boundary failure/order tests. No
// production caller can select a fault, or bypass the actual filesystem calls.
func (s *Store) checkpoint(point string) error {
	if p1testhook.Enabled {
		if err := p1testhook.Record("store", "checkpoint", "", "", "", true, map[string]any{"checkpoint": point}); err != nil {
			return err
		}
	}
	if s.fault != nil {
		return s.fault(point)
	}
	return nil
}

func (s *Store) syncDir(r *os.Root, point string) error {
	if err := s.checkpoint(point); err != nil {
		return err
	}
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func (s *Store) writeNew(r *os.Root, name string, data []byte, point string) error {
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err = s.checkpoint(point + ".write"); err == nil {
		var n int
		n, err = f.Write(data)
		if err == nil && n != len(data) {
			err = fmt.Errorf("short write: %d/%d", n, len(data))
		}
	}
	if err == nil {
		err = s.checkpoint(point + ".sync")
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err == nil {
		err = s.checkpoint(point + ".closed")
	}
	return err
}

func (s *Store) uncertain(err error) error {
	s.poisoned = true
	return errors.Join(ErrUncertain, err)
}

// replaceActive requires the store mutex and writer lock. Temporary and target
// names are relative to the same open state directory, never to runtime/cache.
func (s *Store) replaceActive(data []byte) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	tmp := ".active-" + token
	defer s.root.Remove(tmp) // only our own unpublished temporary file
	if err = s.writeNew(s.root, tmp, data, "active.file"); err != nil {
		return err
	}
	if err = s.checkpoint("active.rename"); err != nil {
		return err
	}
	if err = s.root.Rename(tmp, "active.json"); err != nil {
		return s.uncertain(err)
	}
	if err = s.syncDir(s.root, "active.parent-sync"); err != nil {
		return s.uncertain(err)
	}
	return nil
}
