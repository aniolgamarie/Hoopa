//go:build !linux || !amd64

package acceptedwitness

import (
	"errors"
	"net"
)

func supportedPlatform() error {
	return errors.New("accepted witness: unsupported platform; requires linux/amd64")
}
func captureTCP(*net.TCPConn, uint64) (Snapshot, error) { return Snapshot{}, supportedPlatform() }
