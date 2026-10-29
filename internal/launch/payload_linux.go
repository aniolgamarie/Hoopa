//go:build linux

package launch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrLaunchPayload = errors.New("LAUNCH_PAYLOAD_INVALID")

const (
	launchPayloadMagic        = "HLPAY001"
	launchPayloadMaxBytes     = 2 << 20
	launchPayloadMaxItems     = 4096
	launchPayloadMaxItemBytes = (128 << 10) - 1
)

// launchPayload is private transport material, not a printable launch record.
// Strings are byte sequences here: neither UTF-8 normalization nor shell/path
// interpretation is performed. The caller must not mutate slices concurrently.
type launchPayload struct{ Argv, Env []string }

func (launchPayload) Format(s fmt.State, _ rune)   { _, _ = s.Write([]byte("launch.payload{private}")) }
func (launchPayload) String() string               { return "launch.payload{private}" }
func (launchPayload) GoString() string             { return "launch.payload{private}" }
func (launchPayload) MarshalJSON() ([]byte, error) { return []byte(`"launch.payload{private}"`), nil }

func encodeLaunchPayload(value launchPayload) ([]byte, error) {
	if len(value.Argv) == 0 || len(value.Argv) > launchPayloadMaxItems || len(value.Env) > launchPayloadMaxItems || value.Argv[0] == "" {
		return nil, ErrLaunchPayload
	}
	size := len(launchPayloadMagic) + 8
	for _, values := range [][]string{value.Argv, value.Env} {
		for _, item := range values {
			if len(item) > launchPayloadMaxItemBytes || strings.IndexByte(item, 0) >= 0 || len(item) > launchPayloadMaxBytes-size-4 {
				return nil, ErrLaunchPayload
			}
			size += 4 + len(item)
		}
	}
	raw := make([]byte, 0, size)
	raw = append(raw, launchPayloadMagic...)
	for _, values := range [][]string{value.Argv, value.Env} {
		raw = binary.BigEndian.AppendUint32(raw, uint32(len(values)))
		for _, item := range values {
			raw = binary.BigEndian.AppendUint32(raw, uint32(len(item)))
			raw = append(raw, item...)
		}
	}
	return raw, nil
}

func decodeLaunchPayload(reader io.Reader) (launchPayload, error) {
	var zero launchPayload
	if reader == nil {
		return zero, ErrLaunchPayload
	}
	// Each advertised length/count is checked against both its own fixed bound
	// and the remaining encoded budget before allocating or reading its body.
	remaining := launchPayloadMaxBytes
	read := func(raw []byte) error {
		if len(raw) > remaining {
			return ErrLaunchPayload
		}
		remaining -= len(raw)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return ErrLaunchPayload
		}
		return nil
	}
	var magic [8]byte
	if read(magic[:]) != nil || string(magic[:]) != launchPayloadMagic {
		return zero, ErrLaunchPayload
	}
	var word [4]byte
	var result launchPayload
	for array := 0; array < 2; array++ {
		if read(word[:]) != nil {
			return zero, ErrLaunchPayload
		}
		count := binary.BigEndian.Uint32(word[:])
		reserve := 0
		if array == 0 {
			reserve = 4
		}
		if count > launchPayloadMaxItems || (array == 0 && count == 0) || uint64(count)*4+uint64(reserve) > uint64(remaining) {
			return zero, ErrLaunchPayload
		}
		values := make([]string, 0, int(count))
		for i := uint32(0); i < count; i++ {
			if read(word[:]) != nil {
				return zero, ErrLaunchPayload
			}
			length := binary.BigEndian.Uint32(word[:])
			minimum := uint64(count-i-1)*4 + uint64(reserve)
			if length > launchPayloadMaxItemBytes || uint64(length)+minimum > uint64(remaining) || (array == 0 && i == 0 && length == 0) {
				return zero, ErrLaunchPayload
			}
			item := make([]byte, int(length))
			if read(item) != nil || strings.IndexByte(string(item), 0) >= 0 {
				return zero, ErrLaunchPayload
			}
			values = append(values, string(item))
		}
		if array == 0 {
			result.Argv = values
		} else {
			result.Env = values
		}
	}
	var trailing [1]byte
	if n, err := io.ReadFull(reader, trailing[:]); n != 0 || err != io.EOF {
		return zero, ErrLaunchPayload
	}
	return result, nil
}
