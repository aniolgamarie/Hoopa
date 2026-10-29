//go:build !linux

package subscription

import "context"

// Platform-specific private-source verification is required before reading
// credentials. Cross-platform acquisition remains an explicit P3 obligation.
func readFetchFile(context.Context, string, int, bool) ([]byte, error) {
	return nil, ErrFetchSource
}
