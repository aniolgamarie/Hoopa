//go:build linux && amd64

package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/policy"
	profile "hoopa/protocol/sourceprofile"
)

const codecSettingsJSON = `{"schema_version":1,"identity_key":"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=","runtime":{"controller":"127.0.0.1:32000","secret":"ssssssssssssssssssssssssssssssss","entry":{"name":"shared-entry","type":"mixed","listen":"127.0.0.1","port":32001,"udp":false,"users":[{"username":"shared-user","password":"pppppppppppppppppppppppppppppppp"}]}}}`

func codecSettingsInputs() ([]byte, policy.RuntimeInput) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	runtime := policy.RuntimeInput{Controller: "127.0.0.1:32000", Secret: strings.Repeat("s", 32), Entry: profile.Entry{Name: "shared-entry", Type: "mixed", Listen: "127.0.0.1", Port: 32001, Users: []profile.User{{Username: "shared-user", Password: strings.Repeat("p", 32)}}}}
	return key, runtime
}
func codecSettingsInvalid(t *testing.T, s SourceSettings, err error) {
	t.Helper()
	if err != ErrSourceSettings || !reflect.DeepEqual(s, SourceSettings{}) {
		t.Fatal("must fail with fixed error and zero settings")
	}
}
func TestSourceSettingsCodecGoldenFullShape(t *testing.T) {
	key, runtime := codecSettingsInputs()
	s, err := NewSourceSettings(key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.SensitiveBytes()
	if err != nil || string(raw) != codecSettingsJSON {
		t.Fatal("full canonical shape changed", err)
	}
	// SHA256 computed independently with Python hashlib over the fixed JSON above.
	if s.Digest() != "692117334d26b988a700faa31f676e782cf2ddb9a3a7d33f4c496f9d3ce34b90" {
		t.Fatal("independent digest changed")
	}
	decoded, err := DecodeSourceSettings([]byte(codecSettingsJSON))
	if err != nil || decoded.Digest() != s.Digest() {
		t.Fatal("decode failed", err)
	}
	gotKey, err := decoded.SensitiveIdentityKey()
	if err != nil || !bytes.Equal(gotKey, key) {
		t.Fatal("key changed")
	}
	gotRuntime, err := decoded.Runtime()
	if err != nil || !reflect.DeepEqual(gotRuntime, runtime) {
		t.Fatal("runtime changed")
	}
	changedKey := bytes.Clone(key)
	changedKey[0] ^= 255
	other, err := NewSourceSettings(changedKey, runtime)
	if err != nil || other.Digest() == s.Digest() {
		t.Fatal("key not bound")
	}
	runtime.Secret = strings.Repeat("q", 32)
	other, err = NewSourceSettings(key, runtime)
	if err != nil || other.Digest() == s.Digest() {
		t.Fatal("runtime not bound")
	}
}
func TestSourceSettingsCodecInvalidProfileAndKey(t *testing.T) {
	key, runtime := codecSettingsInputs()
	for _, length := range []int{0, 1, 31, 33, 64} {
		s, err := NewSourceSettings(make([]byte, length), runtime)
		codecSettingsInvalid(t, s, err)
	}
	cases := map[string]func(*policy.RuntimeInput){
		"controller-nonloopback":     func(r *policy.RuntimeInput) { r.Controller = "0.0.0.0:32000" },
		"controller-dns":             func(r *policy.RuntimeInput) { r.Controller = "localhost:32000" },
		"controller-zeroport":        func(r *policy.RuntimeInput) { r.Controller = "127.0.0.1:0" },
		"controller-overport":        func(r *policy.RuntimeInput) { r.Controller = "127.0.0.1:65536" },
		"controller-noncanonical":    func(r *policy.RuntimeInput) { r.Controller = "127.0.0.1:032000" },
		"secret-short":               func(r *policy.RuntimeInput) { r.Secret = "short" },
		"secret-colon":               func(r *policy.RuntimeInput) { r.Secret = strings.Repeat(":", 32) },
		"secret-control":             func(r *policy.RuntimeInput) { r.Secret = strings.Repeat("s", 31) + "\n" },
		"secret-invalidutf8":         func(r *policy.RuntimeInput) { r.Secret = strings.Repeat("s", 31) + string([]byte{255}) },
		"name-empty":                 func(r *policy.RuntimeInput) { r.Entry.Name = "" },
		"type-http":                  func(r *policy.RuntimeInput) { r.Entry.Type = "http" },
		"listen-nonloopback":         func(r *policy.RuntimeInput) { r.Entry.Listen = "0.0.0.0" },
		"entry-zeroport":             func(r *policy.RuntimeInput) { r.Entry.Port = 0 },
		"entry-controller-collision": func(r *policy.RuntimeInput) { r.Entry.Port = 32000 },
		"udp":                        func(r *policy.RuntimeInput) { r.Entry.UDP = true },
		"users-null":                 func(r *policy.RuntimeInput) { r.Entry.Users = nil },
		"users-empty":                func(r *policy.RuntimeInput) { r.Entry.Users = []profile.User{} },
		"users-multiple":             func(r *policy.RuntimeInput) { r.Entry.Users = append(r.Entry.Users, r.Entry.Users[0]) },
		"user-colon":                 func(r *policy.RuntimeInput) { r.Entry.Users[0].Username = "user:other" },
		"password-short":             func(r *policy.RuntimeInput) { r.Entry.Users[0].Password = "short" },
		"password-controller-secret": func(r *policy.RuntimeInput) { r.Entry.Users[0].Password = r.Secret },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			_, r := codecSettingsInputs()
			change(&r)
			s, err := NewSourceSettings(key, r)
			codecSettingsInvalid(t, s, err)
			// A syntactically canonical envelope cannot bypass the shared profile checks.
			var obj map[string]any
			if json.Unmarshal([]byte(codecSettingsJSON), &obj) != nil {
				t.Fatal("fixture")
			}
			obj["runtime"] = map[string]any{"controller": r.Controller, "secret": r.Secret, "entry": r.Entry}
			raw, err := json.Marshal(obj)
			if err != nil {
				t.Fatal(err)
			}
			s, err = DecodeSourceSettings(raw)
			codecSettingsInvalid(t, s, err)
		})
	}
}
func TestSourceSettingsCodecRejectsNonexactRepresentation(t *testing.T) {
	raw := []byte(codecSettingsJSON)
	cases := map[string][]byte{
		"empty": nil, "null": []byte(`null`), "array": []byte(`[]`), "truncated": raw[:len(raw)-1],
		"whitespace": append([]byte(" "), raw...), "newline": append(append([]byte{}, raw...), '\n'), "tail": append(append([]byte{}, raw...), []byte(`{}`)...),
		"unknown":         append([]byte(`{"foreign":"private-material",`), raw[1:]...),
		"duplicate":       append([]byte(`{"schema_version":1,`), raw[1:]...),
		"schema2":         bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":2`), 1),
		"schemafloat":     bytes.Replace(raw, []byte(`"schema_version":1`), []byte(`"schema_version":1.0`), 1),
		"casefold":        bytes.Replace(raw, []byte(`"identity_key"`), []byte(`"IDENTITY_KEY"`), 1),
		"escaped-name":    bytes.Replace(raw, []byte(`"shared-entry"`), []byte(`"shared\u002dentry"`), 1),
		"surrogate":       bytes.Replace(raw, []byte(`"shared-entry"`), []byte(`"\ud800"`), 1),
		"utf8":            bytes.Replace(raw, []byte(`"shared-entry"`), []byte{'"', 255, '"'}, 1),
		"port-exp":        bytes.Replace(raw, []byte(`"port":32001`), []byte(`"port":3.2001e4`), 1),
		"runtime-unknown": bytes.Replace(raw, []byte(`"runtime":{`), []byte(`"runtime":{"foreign":false,`), 1),
		"entry-unknown":   bytes.Replace(raw, []byte(`"entry":{`), []byte(`"entry":{"foreign":false,`), 1),
		"user-unknown":    bytes.Replace(raw, []byte(`"users":[{`), []byte(`"users":[{"foreign":false,`), 1),
		"duplicate-entry": bytes.Replace(raw, []byte(`"port":32001`), []byte(`"port":0,"port":32001`), 1),
		"duplicate-user":  bytes.Replace(raw, []byte(`"username":"shared-user"`), []byte(`"username":"other","username":"shared-user"`), 1),
		"key-badbase64":   bytes.Replace(raw, []byte(`"AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="`), []byte(`"not base64!"`), 1),
		"oversize":        bytes.Repeat([]byte{' '}, config.MaxSourceSettingsBytes+1),
	}
	// Every required field at all nesting levels: omission or explicit null cannot
	// be accepted as an implicit Go zero value (notably udp:false).
	for _, field := range []string{"schema_version", "identity_key", "runtime", "controller", "secret", "entry", "name", "type", "listen", "port", "udp", "users", "username", "password"} {
		prefix := []byte(`"` + field + `":`)
		start := bytes.Index(raw, prefix)
		if start < 0 {
			t.Fatal("field not found")
		}
		valueStart := start + len(prefix)
		decoder := json.NewDecoder(bytes.NewReader(raw[valueStart:]))
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			t.Fatal("invalid fixture field")
		}
		end := valueStart + int(decoder.InputOffset())
		cases["null-"+field] = append(append(append([]byte{}, raw[:valueStart]...), []byte("null")...), raw[end:]...)
		omitStart, omitEnd := start, end
		if raw[end] == ',' {
			omitEnd++
		} else {
			omitStart--
		}
		cases["missing-"+field] = append(append([]byte{}, raw[:omitStart]...), raw[omitEnd:]...)

	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) { s, err := DecodeSourceSettings(b); codecSettingsInvalid(t, s, err) })
	}
}
func TestSourceSettingsCodecPrivateOwnedCopiesAndZero(t *testing.T) {
	var zero SourceSettings
	if b, err := zero.SensitiveBytes(); b != nil || err != ErrSourceSettings {
		t.Fatal("zero bytes")
	}
	if b, err := zero.SensitiveIdentityKey(); b != nil || err != ErrSourceSettings {
		t.Fatal("zero key")
	}
	if r, err := zero.Runtime(); !reflect.DeepEqual(r, policy.RuntimeInput{}) || err != ErrSourceSettings {
		t.Fatal("zero runtime")
	}
	if zero.Digest() != "" {
		t.Fatal("zero digest")
	}
	key, runtime := codecSettingsInputs()
	s, err := NewSourceSettings(key, runtime)
	if err != nil {
		t.Fatal(err)
	}
	copyValue := s
	key[0] ^= 255
	runtime.Entry.Users[0].Password = "changed"
	b, _ := s.SensitiveBytes()
	b[0] = '!'
	k, _ := s.SensitiveIdentityKey()
	k[0] ^= 255
	r, _ := s.Runtime()
	r.Entry.Users[0].Username = "changed"
	for _, v := range []SourceSettings{s, copyValue} {
		b, _ := v.SensitiveBytes()
		if string(b) != codecSettingsJSON {
			t.Fatal("mutable input/getter reached authority")
		}
		r, _ := v.Runtime()
		if r.Entry.Users[0].Username != "shared-user" {
			t.Fatal("runtime alias")
		}
		for _, formatted := range []string{fmt.Sprint(v), fmt.Sprintf("%#v", v)} {
			if formatted != "control.SourceSettings{private material}" {
				t.Fatal("private marker changed")
			}
		}
	}
	raw := []byte(codecSettingsJSON)
	decoded, err := DecodeSourceSettings(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	b, _ = decoded.SensitiveBytes()
	if string(b) != codecSettingsJSON {
		t.Fatal("decode retained caller buffer")
	}
	// Public JSON reflection cannot accidentally serialize the private key/runtime.
	public, err := json.Marshal(decoded)
	if err != nil || string(public) != "{}" {
		t.Fatal("private fields exported")
	}
}
