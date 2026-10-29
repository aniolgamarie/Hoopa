//go:build linux && amd64

package control

import "hoopa/internal/config"

// configurationNeutralHistory admits only proved history that never changed a
// runtime. It extends the empty-runtime Apply guard without weakening the older
// service-stop proof or accepting arbitrary source-shaped Journal records.
// Called under c.mu, after the caller has established that no run exists.
func (c *Coordinator) configurationNeutralHistory(record StoredOperation, base config.ActivePointer) bool {
	if !terminal(record.Record.Phase) {
		return c.liveSourceUpdateLocked(record)
	}
	if record.Record.Compacted && record.Record.Identity.Operation == "operation.recover" {
		return false
	}
	if record.Record.Identity.Operation == "subscription.update" {
		settings, err := c.sourceSettingsLocked()
		if err != nil {
			return false
		}
		_, err = c.sourceEntryLocked(record, settings)
		return err == nil
	}
	if record.Record.Identity.Operation == "operation.recover" {
		request, err := ParseRequest(record.Record.Request)
		if err != nil {
			return false
		}
		p, err := parseRecoverParameters(request)
		if err != nil {
			return false
		}
		target, err := c.journal.Get(p.OperationID)
		if err == nil && target.Record.Identity.Operation == "subscription.update" {
			return record.Record.Phase == Committed && recoveryCallBound(record, target) && c.configurationNeutralHistory(target, base)
		}
	}
	return c.serviceOnlyHistory(record, base)
}

// A live source worker has no runtime authority and cannot publish its candidate
// across Apply: its original revision is rechecked under this same mutex. Dead
// or malformed pending operations still require explicit recovery. The callback
// reads the updater's active map only while the coordinator mutex is held.
func (c *Coordinator) liveSourceUpdateLocked(record StoredOperation) bool {
	if terminal(record.Record.Phase) || record.Record.Identity.Operation != "subscription.update" || c.sourceAlive == nil || !c.sourceAlive(record.Record.Identity.OperationID) {
		return false
	}
	settings, err := c.sourceSettingsLocked()
	if err != nil {
		return false
	}
	_, err = c.sourceEntryLocked(record, settings)
	return err == nil
}
