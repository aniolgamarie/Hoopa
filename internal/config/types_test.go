package config

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIDBoundaryAndIdentityTypes(t *testing.T) {
	for _, s := range []string{"a", "A0._:-", strings.Repeat("x", 128)} {
		if _, err := ParseID(s); err != nil {
			t.Fatalf("valid ID: %v", err)
		}
	}
	for _, s := range []string{"", strings.Repeat("x", 129), "_a", "-a", "a b", "a/", "a\n", "é"} {
		if _, err := ParseID(s); err == nil {
			t.Fatalf("accepted invalid ID %q", s)
		}
	}
	seen := map[reflect.Type]bool{}
	for _, v := range []any{ID("x"), Revision("x"), Generation("x"), RunID("x"), CounterEpoch("x")} {
		typ := reflect.TypeOf(v)
		if seen[typ] {
			t.Fatalf("identity types alias each other: %v", typ)
		}
		seen[typ] = true
	}
	id := ID("kept")
	for _, raw := range []string{`null`, `1`, `"bad space"`, `"unterminated`} {
		if err := json.Unmarshal([]byte(raw), &id); err == nil || id != "kept" {
			t.Fatalf("invalid ID changed receiver: %s", raw)
		}
	}
	if _, err := json.Marshal(ID("invalid space")); err == nil {
		t.Fatal("marshaled invalid ID")
	}
}

func TestTimestampUTCAndFailedDecode(t *testing.T) {
	stamp, err := ParseTimestamp("2026-10-22T21:30:00.123+08:00")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stamp)
	if err != nil || string(raw) != `"2026-10-22T13:30:00.123Z"` || stamp.Time().Location() != time.UTC {
		t.Fatalf("UTC normalization: %s %v", raw, err)
	}
	nanos, err := ParseTimestamp("2026-10-22T13:30:00.123456789Z")
	if err != nil || nanos.Time().Nanosecond() != 123456789 {
		t.Fatalf("nanosecond precision: %v", err)
	}
	before := stamp
	for _, raw := range []string{`null`, `42`, `"2026-13-01T00:00:00Z"`, `"2026-10-22T13:30:00"`, `"2026-10-22T13:30:00+24:00"`, `"2026-10-22T13:30:00+00:60"`, `"2026-10-22T1:30:00Z"`, `"2026-10-22T13:30:00,123Z"`, `"2026-10-22T13:30:00.1234567890Z"`} {
		if err := json.Unmarshal([]byte(raw), &stamp); err == nil || stamp != before {
			t.Fatalf("invalid timestamp changed receiver: %s", raw)
		}
	}
}

func TestDurationBoundaries(t *testing.T) {
	start := time.Now()
	for _, delta := range []time.Duration{0, 999 * time.Microsecond, 1500 * time.Microsecond, time.Duration(1<<63 - 1)} {
		got, err := DurationBetween(start, start.Add(delta))
		if err != nil || got != DurationMS(delta.Milliseconds()) {
			t.Fatalf("duration %v: %v %v", delta, got, err)
		}
	}
	if _, err := DurationBetween(start, start.Add(-time.Nanosecond)); err == nil {
		t.Fatal("accepted negative duration")
	}
	if _, err := DurationBetween(time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("saturated duration: %v", err)
	}
}

func TestExactBytesAndFailedDecode(t *testing.T) {
	for _, s := range []string{"0", "9007199254740993", "18446744073709551615"} {
		b, err := ParseBytes(s)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(b)
		if err != nil || string(raw) != `"`+s+`"` {
			t.Fatalf("lost precision: %s %v", raw, err)
		}
		var decoded Bytes
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != b {
			t.Fatalf("roundtrip: %v", err)
		}
	}
	b := Bytes(7)
	for _, raw := range []string{`null`, `0`, `9007199254740993`, `""`, `"00"`, `"01"`, `"-1"`, `"+1"`, `"1.0"`, `"1e3"`, `" 1"`, `"18446744073709551616"`} {
		if err := json.Unmarshal([]byte(raw), &b); err == nil || b != 7 {
			t.Fatalf("invalid bytes changed receiver: %s", raw)
		}
	}
	if _, err := ParseBytes("18446744073709551616"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("parse overflow: %v", err)
	}
}

func TestCheckedByteArithmeticAndStorage(t *testing.T) {
	if got, err := Bytes(9007199254740992).Add(1); err != nil || got != 9007199254740993 {
		t.Fatalf("exact add: %v %v", got, err)
	}
	if got, err := Bytes(^uint64(0)).Add(1); got != 0 || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("add overflow: %v %v", got, err)
	}
	if got, err := Bytes(1<<63 - 1).Int64(); err != nil || got != 1<<63-1 {
		t.Fatalf("storage max: %v %v", got, err)
	}
	if got, err := Bytes(1 << 63).Int64(); got != 0 || !errors.Is(err, ErrIntegrity) {
		t.Fatalf("storage overflow: %v %v", got, err)
	}
}
