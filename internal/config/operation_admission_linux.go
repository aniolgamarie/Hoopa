//go:build linux

package config

// CheckOperationAdmission checks writer qualification, staging and the new-ID
// capacity before the coordinator publishes an unaccepted candidate. It writes
// nothing. The caller must keep its control lock; CAS still repeats all checks.
func (s *Store) CheckOperationAdmission() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writable(); err != nil {
		return err
	}
	dir, err := openPrivateDir(s.root, "operations", false)
	if err != nil {
		return err
	}
	defer dir.Close()
	ids, staging, err := operationInventory(dir)
	if err != nil {
		return err
	}
	if staging {
		return ErrOperationStaging
	}
	if len(ids) >= MaxOperationFiles {
		return ErrOperationCapacity
	}
	return nil
}
