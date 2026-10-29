package config

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestCanonicalFixtures(t *testing.T) {
	// Independent expected strings, using RFC 8785 sections 3.2.2/3.2.3 rules.
	// Hoopa deliberately rejects RFC examples whose integral float exceeds 2^53-1.
	cases := []struct{ name, input, want string }{
		{"nested-array-order", ` {"z":[{"b":1,"a":2},3,2,1],"a":{"y":null,"x":false}} `, `{"a":{"x":false,"y":null},"z":[{"a":2,"b":1},3,2,1]}`},
		{"utf16-key-order", `{"\ufb33":7,"\u20ac":5,"\r":1,"\ud83d\ude00":6,"\u0080":3,"1":2,"\u00f6":4}`, "{\"\\r\":1,\"1\":2,\"\u0080\":3,\"ö\":4,\"€\":5,\"😀\":6,\"\ufb33\":7}"},
		{"nonBMP-before-private-BMP", `{"\ue000":2,"\ud800\udc00":1}`, "{\"\U00010000\":1,\"\ue000\":2}"},
		{"string-escapes-no-HTML", `"\u0000\u0001\b\t\n\f\r\"\\\/\u001F\u2028\u2029<>&"`, "\"\\u0000\\u0001\\b\\t\\n\\f\\r\\\"\\\\/\\u001f\u2028\u2029<>&\""},
		{"no-normalization", `{"\u00e9":"composed","e\u0301":"decomposed"}`, "{\"e\u0301\":\"decomposed\",\"é\":\"composed\"}"},
		{"paired-surrogates", `{"\uD83D\uDE00":"\uD834\uDD1E"}`, `{"😀":"𝄞"}`},
		{"literal-replacement-character", `"\ufffd"`, "\"\ufffd\""},
		{"BOM-in-string", `"\ufeff"`, "\"\ufeff\""},
		{"case-distinct-keys", `{"a":2,"A":1,"":0}`, `{"":0,"A":1,"a":2}`},
		{"opaque-large-integer-string", `"18446744073709551615"`, `"18446744073709551615"`},
		{"null", " \r\nnull\t ", `null`}, {"bools", `[true,false]`, `[true,false]`}, {"empty-array", `[ ]`, `[]`}, {"empty-object", `{ }`, `{}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input := []byte(c.input)
			saved := append([]byte(nil), input...)
			got, err := CanonicalJSON(input)
			if err != nil || string(got) != c.want {
				t.Fatalf("canonical mismatch got %q want %q err=%v", got, c.want, err)
			}
			if !bytes.Equal(input, saved) {
				t.Fatal("input mutated")
			}
			second, err := CanonicalJSON(got)
			if err != nil || !bytes.Equal(second, got) {
				t.Fatal("canonical output is not idempotent", err)
			}
		})
	}
}

func TestCanonicalRFCNumbers(t *testing.T) {
	// Appendix B values within Hoopa's additional safe-integral-float restriction,
	// plus independently specified decimal grammar and ECMAScript thresholds.
	cases := []struct{ input, want string }{
		{"0", "0"}, {"-0", "0"}, {"-0.000e+09", "0"}, {"5e-324", "5e-324"}, {"-5e-324", "-5e-324"},
		{"9.999999999999997e-7", "9.999999999999997e-7"}, {"1e-6", "0.000001"}, {"1.0000000000000002e-6", "0.0000010000000000000002"},
		{"333333333.3333332", "333333333.3333332"}, {"333333333.33333325", "333333333.33333325"}, {"333333333.33333329", "333333333.3333333"}, {"333333333.3333334", "333333333.3333334"}, {"333333333.33333343", "333333333.33333343"},
		{"-0.0000033333333333333333", "-0.0000033333333333333333"}, {"1424953923781206.25", "1424953923781206.2"},
		{"4.50", "4.5"}, {"2e-3", "0.002"}, {"1.2300e+003", "1230"}, {"1E-027", "1e-27"},
		{"9007199254740991", "9007199254740991"}, {"-9007199254740991.0", "-9007199254740991"}, {"1e15", "1000000000000000"},
		{"1e-9999", "0"}, {"0e99999", "0"},
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got, err := CanonicalJSON([]byte(c.input))
			if err != nil || string(got) != c.want {
				t.Fatalf("got %q want %q err=%v", got, c.want, err)
			}
			again, err := CanonicalJSON(got)
			if err != nil || !bytes.Equal(got, again) {
				t.Fatal("numeric idempotence", err)
			}
		})
	}
}

func TestCanonicalRejectsAmbiguityAndInvalidInput(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{"empty", nil}, {"whitespace", []byte(" \r\n\t")}, {"duplicate", []byte(`{"a":1,"a":2}`)},
		{"escaped-duplicate", []byte(`{"a":1,"\u0061":2}`)}, {"nested-duplicate", []byte(`[{"x":{"z":1,"\u007a":2}}]`)},
		{"surrogate-key-duplicate", []byte(`{"😀":0,"\ud83d\ude00":1}`)}, {"slash-key-duplicate", []byte(`{"/":0,"\/":1}`)},
		{"empty-key-duplicate", []byte(`{"":0,"":1}`)}, {"nul-key-duplicate", []byte(`{"\u0000":0,"\u0000":1}`)},
		{"high-surrogate", []byte(`"\ud800"`)}, {"low-surrogate", []byte(`"\udfff"`)}, {"high-high", []byte(`"\ud800\ud801"`)},
		{"high-ordinary", []byte(`"\ud800\u0041"`)}, {"high-literal", []byte(`"\ud800x"`)}, {"high-raw-nonBMP", []byte(`"\ud800😀"`)},
		{"surrogate-key", []byte(`{"\udc00":null}`)}, {"invalid-UTF8", []byte{'"', 0xff, '"'}}, {"encoded-surrogate", []byte{'"', 0xed, 0xa0, 0x80, '"'}}, {"overlong-UTF8", []byte{'"', 0xc0, 0xaf, '"'}},
		{"literal-control", []byte("\"a\nb\"")}, {"bad-escape", []byte(`"\x41"`)}, {"short-unicode", []byte(`"\u123"`)}, {"nonhex", []byte(`"\uXX00"`)}, {"unterminated", []byte(`"unfinished`)},
		{"trailing-value", []byte(`{} []`)}, {"trailing-text", []byte(`nullx`)}, {"trailing-comma", []byte(`[0,]`)}, {"object-trailing-comma", []byte(`{"a":0,}`)}, {"missing-colon", []byte(`{"a" 0}`)}, {"unquoted-key", []byte(`{a:0}`)}, {"missing-array-comma", []byte(`[0 1]`)},
		{"leading-plus", []byte(`+1`)}, {"leading-zero", []byte(`01`)}, {"negative-leading-zero", []byte(`-01`)}, {"empty-fraction", []byte(`1.`)}, {"empty-exponent", []byte(`1e+`)}, {"leading-dot", []byte(`.1`)},
		{"NaN", []byte(`NaN`)}, {"Infinity", []byte(`Infinity`)}, {"negative-infinity", []byte(`-Infinity`)}, {"overflow", []byte(`1e309`)},
		// These are Hoopa restrictions, not a claim that RFC 8785 rejects finite big values.
		{"Hoopa-unsafe-integer", []byte(`9007199254740992`)}, {"Hoopa-negative-unsafe", []byte(`-9007199254740992`)}, {"Hoopa-rounded-outside", []byte(`9007199254740991.5`)},
		{"Hoopa-big-scientific", []byte(`1e21`)}, {"Hoopa-RFC-big-value", []byte(`1.7976931348623157e308`)},
		{"BOM-prefix", []byte("\xef\xbb\xbf0")}, {"comment", []byte(`/*comment*/0`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := CanonicalJSON(c.raw)
			if !errors.Is(err, ErrCanonicalJSON) || err != ErrCanonicalJSON || got != nil {
				t.Fatalf("invalid input accepted or returned nonconstant error: %v", err)
			}
		})
	}
}

func TestCanonicalLimits(t *testing.T) {
	t.Run("exact-input-limit", func(t *testing.T) {
		raw := []byte(`"` + strings.Repeat("x", MaxCanonicalBytes-2) + `"`)
		got, err := CanonicalJSON(raw)
		if err != nil || !bytes.Equal(got, raw) {
			t.Fatal("exact input bound", err)
		}
	})
	t.Run("over-input-limit", func(t *testing.T) {
		raw := []byte("0" + strings.Repeat(" ", MaxCanonicalBytes))
		if got, err := CanonicalJSON(raw); got != nil || err != ErrCanonicalJSON {
			t.Fatal("over input bound", err)
		}
	})
	for _, kind := range []string{"array", "object"} {
		t.Run(kind+"-depth", func(t *testing.T) {
			open, close := "[", "]"
			if kind == "object" {
				open, close = `{"":`, `}`
			}
			raw := []byte(strings.Repeat(open, MaxCanonicalDepth) + "null" + strings.Repeat(close, MaxCanonicalDepth))
			if got, err := CanonicalJSON(raw); err != nil || !bytes.Equal(got, raw) {
				t.Fatal("exact depth bound", err)
			}
			raw = []byte(open + string(raw) + close)
			if got, err := CanonicalJSON(raw); got != nil || err != ErrCanonicalJSON {
				t.Fatal("excess nesting accepted", err)
			}
		})
	}
	t.Run("output-expansion-not-input-budget", func(t *testing.T) {
		n := (MaxCanonicalBytes - 1) / 5
		raw := []byte("[" + strings.TrimSuffix(strings.Repeat("1e15,", n), ",") + "]")
		want := []byte("[" + strings.TrimSuffix(strings.Repeat("1000000000000000,", n), ",") + "]")
		if len(raw) > MaxCanonicalBytes || len(want) <= MaxCanonicalBytes {
			t.Fatal("fixture bounds")
		}
		got, err := CanonicalJSON(raw)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatal("valid bounded input with expanded output rejected", err)
		}
		// The expanded canonical bytes exceed the API input budget, so a second
		// call correctly rejects them; idempotence applies when output fits input.
	})
}

func TestCanonicalIndependentBuffers(t *testing.T) {
	raw := []byte(`{"b":2,"a":1}`)
	saved := append([]byte(nil), raw...)
	first, err := CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = '['
	if !bytes.Equal(raw, saved) || string(second) != `{"a":1,"b":2}` {
		t.Fatal("input/result buffers alias")
	}
}
