package coreadmission

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func executableABI(path []byte) ([]byte, Identity) {
	raw := make([]byte, 296)
	le.PutUint64(raw, 9007199254740993)
	le.PutUint32(raw[8:], 10)
	le.PutUint32(raw[12:], 11)
	le.PutUint32(raw[16:], uint32(len(path)+1))
	le.PutUint32(raw[20:], 1)
	le.PutUint64(raw[24:], 150)
	le.PutUint64(raw[32:], 9007199254740995)
	copy(raw[40:], path)
	if len(path) < 255 {
		raw[295] = 0xa5
	}
	return raw, Identity{ClientCookie: "9007199254740993", TGID: 10, CallerTID: 11, LeaderStartBoottimeNS: "100", ObservedBootNS: "200"}
}
func TestExecutableValidAndValueOwnership(t *testing.T) {
	raw, id := executableABI([]byte("/bin/python3 (deleted)"))
	original := append([]byte(nil), raw...)
	got, err := executableIdentity(raw, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Schema != "sourceidentity-executable-call-v1" || got.Status != "unverified" || got.Source != "task_exe_file_d_path_v1" || got.NameSource != "executable_path_basename_v1" || got.CallToken != "9007199254740995" || got.ObservedBootNS != "150" || got.PathText != "/bin/python3 (deleted)" || got.ProgramName != "python3 (deleted)" || !got.PathTextValid || !got.ProgramNameTextValid || got.RawRecord != hex.EncodeToString(original) {
		t.Fatal("value lost", got)
	}
	raw[40] = 'x'
	id.ClientCookie = "other"
	copy := got
	copy.PathText = "changed"
	if got.RawRecord != hex.EncodeToString(original) || got.PathText != "/bin/python3 (deleted)" {
		t.Fatal("mutable alias")
	}
	typ := reflect.TypeOf(got)
	for i := 0; i < typ.NumField(); i++ {
		k := typ.Field(i).Type.Kind()
		if k != reflect.String && k != reflect.Bool {
			t.Fatal("not pure value")
		}
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != 13 {
		t.Fatal("JSON shape")
	}
	for _, key := range strings.Fields("schema status source name_source call_token observed_boot_ns path_raw_hex path_text path_text_valid program_name_raw_hex program_name program_name_text_valid raw_record") {
		if _, ok := fields[key]; !ok {
			t.Fatal("JSON tag", key)
		}
	}
}
func TestExecutablePathAndUTF8Boundaries(t *testing.T) {
	for _, path := range [][]byte{[]byte("/a"), []byte("/路径/程序"), []byte("/" + strings.Repeat("a", 254)), {'/', 0xff, '/', 'o', 'k'}, {'/', 'x', '/', 0xff}} {
		raw, id := executableABI(path)
		got, err := executableIdentity(raw, id)
		if err != nil {
			t.Fatal("valid path", err)
		}
		name := path[bytes.LastIndexByte(path, '/')+1:]
		if got.PathRawHex != hex.EncodeToString(path) || got.ProgramNameRawHex != hex.EncodeToString(name) || got.PathTextValid != utf8.Valid(path) || got.ProgramNameTextValid != utf8.Valid(name) {
			t.Fatal("raw/validity changed")
		}
		if utf8.Valid(path) {
			if got.PathText != string(path) {
				t.Fatal("path text")
			}
		} else if got.PathText != "" {
			t.Fatal("invalid path replaced")
		}
		if utf8.Valid(name) {
			if got.ProgramName != string(name) {
				t.Fatal("name text")
			}
		} else if got.ProgramName != "" {
			t.Fatal("invalid basename replaced")
		}
	}
	for _, path := range [][]byte{[]byte("/"), []byte("relative"), []byte("/bin/"), {'/', 'a', 0, 'b'}} {
		raw, id := executableABI(path)
		if _, err := executableIdentity(raw, id); err == nil {
			t.Fatal("invalid path accepted", path)
		}
	}
}
func TestExecutableStrictABIAndIdentity(t *testing.T) {
	raw, id := executableABI([]byte("/bin/python3"))
	for _, n := range []int{0, 40, 288, 295, 297} {
		b := make([]byte, n)
		copy(b, raw)
		if got, err := executableIdentity(b, id); err == nil || got != (Executable{}) {
			t.Fatal("length accepted", n)
		}
	}
	for i := 0; i < 24; i++ {
		b := append([]byte(nil), raw...)
		b[i] ^= 0x80
		if got, err := executableIdentity(b, id); err == nil || got != (Executable{}) {
			t.Fatal("ABI mutation accepted", i)
		}
	}
	for _, n := range []uint32{0, 1, 257, 0x7fffffff, 0x80000000, 0xffffffff} {
		b := append([]byte(nil), raw...)
		le.PutUint32(b[16:], n)
		if _, err := executableIdentity(b, id); err == nil {
			t.Fatal("path_result accepted", n)
		}
	}
	for _, n := range []uint32{0, 2, ^uint32(0)} {
		b := append([]byte(nil), raw...)
		le.PutUint32(b[20:], n)
		if _, err := executableIdentity(b, id); err == nil {
			t.Fatal("consumed accepted", n)
		}
	}
	for _, n := range []uint64{0, 99, 201, ^uint64(0)} {
		b := append([]byte(nil), raw...)
		le.PutUint64(b[24:], n)
		if _, err := executableIdentity(b, id); err == nil {
			t.Fatal("BOOT accepted", n)
		}
	}
	b := append([]byte(nil), raw...)
	le.PutUint64(b[32:], 0)
	if _, err := executableIdentity(b, id); err == nil {
		t.Fatal("zero token")
	}
	b = append([]byte(nil), raw...)
	b[52] = 1
	if _, err := executableIdentity(b, id); err == nil {
		t.Fatal("missing NUL")
	}
	for _, change := range []func(*Identity){func(i *Identity) { i.ClientCookie = "0" }, func(i *Identity) { i.ClientCookie = "09007199254740993" }, func(i *Identity) { i.ClientCookie = "18446744073709551616" }, func(i *Identity) { i.ClientCookie = "9007199254740994" }, func(i *Identity) { i.TGID++ }, func(i *Identity) { i.CallerTID++ }, func(i *Identity) { i.LeaderStartBoottimeNS = "0" }, func(i *Identity) { i.LeaderStartBoottimeNS = "151" }, func(i *Identity) { i.ObservedBootNS = "149" }, func(i *Identity) { i.ObservedBootNS = "invalid" }} {
		bad := id
		change(&bad)
		if _, err := executableIdentity(raw, bad); err == nil {
			t.Fatal("identity mismatch accepted", bad)
		}
	}
}
func TestExecutableUint64AndInclusiveTimes(t *testing.T) {
	for _, n := range []uint64{1, 9007199254740993, ^uint64(0)} {
		raw, id := executableABI([]byte("/a"))
		le.PutUint64(raw, n)
		le.PutUint64(raw[24:], n)
		le.PutUint64(raw[32:], n)
		s := strconv.FormatUint(n, 10)
		id.ClientCookie = s
		id.LeaderStartBoottimeNS = s
		id.ObservedBootNS = s
		got, err := executableIdentity(raw, id)
		if err != nil || got.ObservedBootNS != s || got.CallToken != s {
			t.Fatal("precision/inclusive interval", n, err)
		}
	}
	for _, n := range []uint64{100, 200} {
		raw, id := executableABI([]byte("/a"))
		le.PutUint64(raw[24:], n)
		if _, err := executableIdentity(raw, id); err != nil {
			t.Fatal("inclusive endpoints", err)
		}
	}
}
