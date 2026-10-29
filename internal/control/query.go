package control

// IsReadOnlyOperation lists only implemented query use cases. Their request IDs
// correlate responses; they never participate in mutation admission or replay.
func IsReadOnlyOperation(operation string) bool {
	switch operation {
	case "status.get", "doctor.get", "operation.get", "subscription.list", "config.plan":
		return true
	default:
		return false
	}
}
