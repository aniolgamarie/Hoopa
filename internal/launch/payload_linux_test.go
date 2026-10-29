//go:build linux

package launch

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestLaunchPayloadIndependentWireAndRawBytes(t *testing.T) {
	// Hand-authored format vector: argv=["x",""]; env=["K=v"].
	expected, err := hex.DecodeString("484c5041593030310000000200000001780000000000000001000000034b3d76")
	if err != nil {
		t.Fatal(err)
	}
	value := launchPayload{Argv: []string{"x", ""}, Env: []string{"K=v"}}
	raw, err := encodeLaunchPayload(value)
	if err != nil || !bytes.Equal(raw, expected) {
		t.Fatalf("wire vector mismatch: %v", err)
	}
	decoded, err := decodeLaunchPayload(bytes.NewReader(expected))
	if err != nil || !reflect.DeepEqual(decoded.Argv, value.Argv) || !reflect.DeepEqual(decoded.Env, value.Env) {
		t.Fatalf("vector decode: %v", err)
	}
	value = launchPayload{Argv: []string{"/literal path", string([]byte{255, 254, 'x'}), "a; $(not-shell)", ""}, Env: []string{"A=first", "A=last", "", "NO_EQUALS", string([]byte{'V', '=', 128})}}
	raw, err = encodeLaunchPayload(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeLaunchPayload(&payloadByteReader{raw: raw})
	if err != nil || !reflect.DeepEqual(decoded, value) {
		t.Fatalf("raw byte/order roundtrip: %v", err)
	}
	value.Argv[0] = "changed"
	value.Env[0] = "changed"
	raw[16] = '!'
	if decoded.Argv[0] != "/literal path" || decoded.Env[0] != "A=first" {
		t.Fatal("returned material aliases input")
	}
	for _, env := range [][]string{nil, {}} {
		raw, err = encodeLaunchPayload(launchPayload{Argv: []string{"x"}, Env: env})
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeLaunchPayload(bytes.NewReader(raw))
		if err != nil || got.Env == nil || len(got.Env) != 0 {
			t.Fatal("empty env must decode to explicit nonnil array")
		}
	}
}

type payloadByteReader struct{ raw []byte }

func (r *payloadByteReader) Read(p []byte) (int, error) {
	if len(r.raw) == 0 {
		return 0, io.EOF
	}
	p[0] = r.raw[0]
	r.raw = r.raw[1:]
	return 1, nil
}

func TestLaunchPayloadRejectsMalformedWireAndReaderErrors(t *testing.T) {
	valid, _ := hex.DecodeString("484c50415930303100000001000000017800000000")
	cases := map[string][]byte{
		"magic":          append([]byte("OTHER001"), valid[8:]...),
		"empty argv":     append([]byte("HLPAY001"), 0, 0, 0, 0, 0, 0, 0, 0),
		"huge count":     append([]byte("HLPAY001"), 255, 255, 255, 255),
		"huge length":    append([]byte("HLPAY001"), 0, 0, 0, 1, 255, 255, 255, 255),
		"empty argv0":    append([]byte("HLPAY001"), 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0),
		"nul":            append(append([]byte{}, valid[:16]...), 0, 0, 0, 0, 0),
		"trailing":       append(append([]byte{}, valid...), 9),
		"nul second arg": append([]byte("HLPAY001"), 0, 0, 0, 2, 0, 0, 0, 1, 'x', 0, 0, 0, 1, 0, 0, 0, 0, 0),
		"nul env":        append([]byte("HLPAY001"), 0, 0, 0, 1, 0, 0, 0, 1, 'x', 0, 0, 0, 1, 0, 0, 0, 1, 0),
	}
	for n := 0; n < len(valid); n++ {
		cases[fmt.Sprintf("truncated%d", n)] = valid[:n]
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := decodeLaunchPayload(bytes.NewReader(raw))
			if !errors.Is(err, ErrLaunchPayload) || got.Argv != nil || got.Env != nil {
				t.Fatal("malformed input accepted or partial payload returned")
			}
		})
	}
	for _, r := range []io.Reader{nil, payloadErrorReader{}, io.MultiReader(bytes.NewReader(valid), payloadErrorReader{})} {
		got, err := decodeLaunchPayload(r)
		if err != ErrLaunchPayload || got.Argv != nil || got.Env != nil {
			t.Fatal("reader error not redacted/zero")
		}
	}
}

type payloadErrorReader struct{}

func (payloadErrorReader) Read([]byte) (int, error) { return 0, errors.New("private-reader-detail") }

type payloadBudgetReader struct {
	prefix []byte
	calls  int
}

func (r *payloadBudgetReader) Read(p []byte) (int, error) {
	r.calls++
	if len(r.prefix) == 0 {
		return 0, errors.New("must not read body")
	}
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	return n, nil
}

func TestLaunchPayloadLimitsBeforeBodyReads(t *testing.T) {
	for _, prefix := range [][]byte{
		append([]byte("HLPAY001"), 0, 0, 16, 1),            // 4097 items
		append([]byte("HLPAY001"), 0, 0, 0, 1, 0, 2, 0, 0), // 128KiB item
	} {
		r := &payloadBudgetReader{prefix: prefix}
		_, err := decodeLaunchPayload(r)
		if err != ErrLaunchPayload || r.calls > 3 {
			t.Fatal("invalid header caused body read")
		}
	}
	maxItem := strings.Repeat("v", 131071)
	for _, value := range []launchPayload{
		{}, {Argv: []string{""}}, {Argv: []string{"a\x00b"}}, {Argv: []string{"x"}, Env: []string{"a\x00b"}},
		{Argv: make([]string, 4097)}, {Argv: []string{"x"}, Env: make([]string, 4097)},
		{Argv: []string{maxItem + "x"}}, {Argv: []string{"x"}, Env: []string{maxItem + "x"}},
	} {
		raw, err := encodeLaunchPayload(value)
		if err != ErrLaunchPayload || raw != nil {
			t.Fatal("invalid value encoded")
		}
	}
	value := launchPayload{Argv: []string{maxItem}, Env: make([]string, 4096)}
	raw, err := encodeLaunchPayload(value)
	if err != nil {
		t.Fatal("inclusive count/item limit rejected")
	}
	if _, err = decodeLaunchPayload(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	// Derive only a byte budget, not encoding logic: 21-byte single "x" argv
	// baseline; each environment item adds 4 bytes of length plus raw content.
	value = launchPayload{Argv: []string{"x"}, Env: []string{}}
	remaining := (2 << 20) - 21
	for remaining > 0 {
		n := remaining - 4
		if n > 131071 {
			n = 131071
		}
		value.Env = append(value.Env, strings.Repeat("e", n))
		remaining -= 4 + n
	}
	raw, err = encodeLaunchPayload(value)
	if err != nil || len(raw) != 2<<20 {
		t.Fatal("exact total bound rejected")
	}
	if _, err = decodeLaunchPayload(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	value.Env[len(value.Env)-1] += "e"
	if raw, err = encodeLaunchPayload(value); err != ErrLaunchPayload || raw != nil {
		t.Fatal("total overflow accepted")
	}
	// Independent too-large wire: sixteen 131071-byte items exceed 2MiB when
	// joined with a large argv, even though each individual header is legal.
	oversized := append([]byte("HLPAY001"), 0, 0, 0, 1, 0, 1, 255, 255)
	oversized = append(oversized, []byte(maxItem)...)
	oversized = append(oversized, 0, 0, 0, 16)
	for i := 0; i < 16; i++ {
		oversized = append(oversized, 0, 1, 255, 255)
		oversized = append(oversized, []byte(maxItem)...)
	}
	if got, err := decodeLaunchPayload(bytes.NewReader(oversized)); err != ErrLaunchPayload || got.Argv != nil || got.Env != nil {
		t.Fatal("wire total overflow accepted")
	}
}

func TestLaunchPayloadFormattingIsPrivate(t *testing.T) {
	value := launchPayload{Argv: []string{"credential-private"}, Env: []string{"PASSWORD=credential-private"}}
	for _, form := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d"} {
		for _, v := range []any{value, &value, struct{ Value launchPayload }{value}} {
			if strings.Contains(fmt.Sprintf(form, v), "credential-private") {
				t.Fatalf("format leaked: %s", form)
			}
		}
	}
	raw, err := json.Marshal(value)
	if err != nil || bytes.Contains(raw, []byte("credential-private")) {
		t.Fatal("JSON leaked payload")
	}
	if ErrLaunchPayload.Error() != "LAUNCH_PAYLOAD_INVALID" {
		t.Fatal("nonconstant error")
	}
}
