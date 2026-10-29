package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

// Identity types have separate meanings and require explicit conversion.
type ID string
type Revision string
type Generation string
type RunID string
type CounterEpoch string

// ErrIntegrity identifies an arithmetic or storage range failure.
var ErrIntegrity = errors.New("INTEGRITY_ERROR")

// ParseID accepts exactly [A-Za-z0-9][A-Za-z0-9._:-]{0,127}.
func ParseID(s string) (ID, error) {
	if len(s) == 0 || len(s) > 128 {
		return "", errors.New("ID must contain 1–128 ASCII token characters")
	}
	for i, c := range []byte(s) {
		alnum := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		if !alnum && (i == 0 || c != '.' && c != '_' && c != ':' && c != '-') {
			return "", errors.New("invalid ID token")
		}
	}
	return ID(s), nil
}

func (id ID) MarshalJSON() ([]byte, error) {
	if _, err := ParseID(string(id)); err != nil {
		return nil, err
	}
	return json.Marshal(string(id))
}

func (id *ID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseID(s)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Timestamp accepts RFC3339 with at most nine fractional digits (nanoseconds),
// rejecting excess precision and normalizing valid offsets to UTC.
type Timestamp struct{ value time.Time }

var timestampForm = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3]):[0-5][0-9])$`)

func ParseTimestamp(s string) (Timestamp, error) {
	if !timestampForm.MatchString(s) {
		return Timestamp{}, errors.New("invalid RFC3339 timestamp or excess precision")
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return Timestamp{}, err
	}
	return Timestamp{t.UTC()}, nil
}

func (t Timestamp) Time() time.Time { return t.value }

func (t Timestamp) MarshalJSON() ([]byte, error) { return t.value.UTC().MarshalJSON() }

func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseTimestamp(s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}

// DurationMS is elapsed milliseconds; sub-millisecond remainders are truncated.
type DurationMS int64

// DurationBetween uses the monotonic readings when both times carry them.
func DurationBetween(start, end time.Time) (DurationMS, error) {
	delta := end.Sub(start)
	if delta < 0 {
		return 0, errors.New("negative duration")
	}
	// Sub saturates outside time.Duration's range; never report that as a value.
	if !start.Add(delta).Equal(end) {
		return 0, fmt.Errorf("%w: duration overflow", ErrIntegrity)
	}
	return DurationMS(delta.Milliseconds()), nil
}

// Bytes is a known exact count, not an unknown/null amount or a ledger record.
type Bytes uint64

func ParseBytes(s string) (Bytes, error) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, errors.New("invalid decimal byte string")
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid decimal byte string")
		}
	}
	value, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: byte count overflow", ErrIntegrity)
	}
	return Bytes(value), nil
}

func (b Bytes) MarshalJSON() ([]byte, error) { return json.Marshal(strconv.FormatUint(uint64(b), 10)) }

func (b *Bytes) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseBytes(s)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

func (b Bytes) Add(other Bytes) (Bytes, error) {
	if uint64(b) > ^uint64(0)-uint64(other) {
		return 0, fmt.Errorf("%w: byte addition overflow", ErrIntegrity)
	}
	return b + other, nil
}

// Int64 checks the range before converting a count to a storage integer.
func (b Bytes) Int64() (int64, error) {
	if uint64(b) > 1<<63-1 {
		return 0, fmt.Errorf("%w: byte count exceeds int64 storage", ErrIntegrity)
	}
	return int64(b), nil
}
