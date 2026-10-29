package document

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeRepresentations(t *testing.T) {
	a, err := Decode([]byte(`{"host":"example.test","copy":"example.test","on":false,"n":1.0,"none":null}`), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Decode([]byte("host: &host example.test\ncopy: *host\non: false\nn: 1.0\nnone: null\n"), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("different values: %#v %#v", a, b)
	}
	if _, ok := a.(map[string]any)["n"].(json.Number); !ok {
		t.Fatal("number lost exact representation")
	}
}
func TestDecodeRejects(t *testing.T) {
	for _, s := range []string{"", "a: 1\na: 2", "a: 1\n---\nb: 2", "1: x", "? [a,b]\n: c", "a: !secret token", "a: !!timestamp 2020-01-01", "a: !!binary dGVzdA==", "a: &a [*a]", "a: &a {x: 1}\nb: {<<: *a}", "a: .nan", "a: .inf", `{"a":"\uD800"}`, `{"a":"\uDC00"}`, string([]byte{'a', ':', ' ', 0xff})} {
		t.Run(s, func(t *testing.T) {
			if _, err := Decode([]byte(s), Limits{}); err == nil {
				t.Fatal("accepted invalid document")
			} else if err.Error() != ErrInvalid.Error() && err.Error() != ErrLimit.Error() {
				t.Fatal("nonconstant error")
			}
		})
	}
	if _, err := Decode([]byte(`{"s":"\uD83D\uDE00"}`), Limits{}); err != nil {
		t.Fatal(err)
	}
}
func TestDecodeLimits(t *testing.T) {
	cases := []struct {
		raw    string
		limits Limits
	}{{"a: 1", Limits{MaxBytes: 3}}, {"[[[0]]]", Limits{MaxDepth: 2}}, {"a: &a value\nb: *a\nc: *a", Limits{MaxAliases: 1}}, {"a: &a [1,2]\nb: *a", Limits{MaxValues: 8}}, {strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), Limits{}}}
	for _, c := range cases {
		if _, err := Decode([]byte(c.raw), c.limits); err == nil {
			t.Fatalf("limit accepted: %+v", c.limits)
		}
	}
	if _, err := Decode([]byte("a: 1"), Limits{MaxBytes: -1}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func TestDecodeAliasExpansion(t *testing.T) {
	raw := []byte("a: &a [x,x]\nb: &b [*a,*a]\nc: [*b,*b]")
	if _, err := Decode(raw, Limits{MaxValues: 20}); !errors.Is(err, ErrLimit) {
		t.Fatalf("expanded values: %v", err)
	}
	if _, err := Decode(raw, Limits{MaxDepth: 3}); err == nil {
		t.Fatal("expanded depth accepted")
	}
}

func TestDecodeNumberLiteralLimit(t *testing.T) {
	for _, size := range []int{128, 129} {
		literal := strings.Repeat("1", size)
		for _, raw := range []string{`{"n":` + literal + `}`, "n: " + literal, "n: !!int " + literal, "n: !!float " + literal} {
			_, err := Decode([]byte(raw), Limits{})
			if size == 128 && err != nil {
				t.Fatalf("128-byte literal rejected: %v", err)
			}
			if size == 129 && !errors.Is(err, ErrLimit) {
				t.Fatalf("129-byte literal: %v", err)
			}
		}
	}
	// Raising the document byte limit must not permit large integer conversion.
	if _, err := Decode([]byte("n: !!int "+strings.Repeat("9", 1<<20)), Limits{MaxBytes: 2 << 20}); !errors.Is(err, ErrLimit) {
		t.Fatalf("large explicit integer: %v", err)
	}
	for _, raw := range []string{`{"n":9007199254740991}`, "n: 9007199254740991", `{"n":"` + strings.Repeat("1", 129) + `"}`, "n: '" + strings.Repeat("1", 129) + "'"} {
		if _, err := Decode([]byte(raw), Limits{}); err != nil {
			t.Fatalf("safe number or string: %v", err)
		}
	}
}
