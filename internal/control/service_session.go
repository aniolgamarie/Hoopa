//go:build linux && amd64

package control

import (
	"crypto/rand"
	"encoding/hex"
	"os"

	"hoopa/internal/config"
)

func serviceInstance(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 24 && hex.EncodeToString(b) == value
}

// OpenServiceRuntime binds exactly one management incarnation to this actual
// writer lease. A value copy cannot mint another session. The returned runtime
// root belongs to the caller; close endpoint resources before Coordinator.Close.
// The session is not a core identity or an automatic restart authorization.
func (c *Coordinator) OpenServiceRuntime() (string, *os.Root, error) {
	if c == nil || c.coordinatorState == nil {
		return "", nil, config.ErrClosed
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil || c.serviceQuiesced {
		return "", nil, config.ErrClosed
	}
	if c.serviceID != "" {
		return "", nil, ErrConflict
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", nil, err
	}
	root, err := c.store.OpenServiceRuntime()
	if err != nil {
		return "", nil, err
	}
	c.serviceID = hex.EncodeToString(nonce[:])
	return c.serviceID, root, nil
}
