package sourcecontrol

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// validUnicode rejects encoding/json's replacement of malformed UTF-8 or lone
// UTF-16 surrogate escapes. Valid escaped keys are allowed; duplicate detection
// below compares decoded key values, including equivalent escape spellings.
func validUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	hex4 := func(value []byte) (uint16, bool) {
		if len(value) < 4 {
			return 0, false
		}
		var result uint16
		for _, c := range value[:4] {
			result <<= 4
			switch {
			case c >= '0' && c <= '9':
				result |= uint16(c - '0')
			case c >= 'a' && c <= 'f':
				result |= uint16(c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				result |= uint16(c - 'A' + 10)
			default:
				return 0, false
			}
		}
		return result, true
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for ; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return false
			}
			if raw[i] != 'u' {
				continue
			}
			code, ok := hex4(raw[i+1:])
			if !ok {
				return false
			}
			i += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			if code >= 0xd800 && code <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return false
				}
				low, ok := hex4(raw[i+3:])
				if !ok || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
		if i >= len(raw) {
			return false
		}
	}
	return true
}

// wireShape is confined to these wire structs: every field is required except
// the explicitly omitempty receipt and probe pointers. It never infers requiredness
// from zero values and rejects ambiguity before encoding/json populates values.
func wireShape(raw []byte, t reflect.Type) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return ErrInvalid
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int:
		if string(raw) != "2" {
			return ErrInvalid
		}
		return nil
	case reflect.Uint64:
		// Probe numbers must be literal canonical unsigned decimal integers, never
		// floats/exponents/signs. Check safe range before assignment and narrower
		// profile limits in Probe.Validate after decoding.
		if len(raw) < 1 || len(raw) > 16 || len(raw) > 1 && raw[0] == '0' {
			return ErrInvalid
		}
		for _, c := range raw {
			if c < '0' || c > '9' {
				return ErrInvalid
			}
		}
		value, err := strconv.ParseUint(string(raw), 10, 64)
		if err != nil || value > 9007199254740991 {
			return ErrInvalid
		}
		return nil
	case reflect.String:
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalid
		}
		return nil
	case reflect.Struct:
		fields := map[string]reflect.StructField{}
		required := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			tag := strings.Split(field.Tag.Get("json"), ",")
			fields[tag[0]] = field
			if len(tag) == 1 {
				required[tag[0]] = true
			}
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		token, err := d.Token()
		if err != nil || token != json.Delim('{') {
			return ErrInvalid
		}
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			field, known := fields[key]
			if err != nil || !ok || !known || seen[key] {
				return ErrInvalid
			}
			seen[key] = true
			var value json.RawMessage
			if d.Decode(&value) != nil || wireShape(value, field.Type) != nil {
				return ErrInvalid
			}
		}
		if token, err = d.Token(); err != nil || token != json.Delim('}') {
			return ErrInvalid
		}
		if _, err = d.Token(); err != io.EOF {
			return ErrInvalid
		}
		for key := range required {
			if !seen[key] {
				return ErrInvalid
			}
		}
		return nil
	case reflect.Slice:
		d := json.NewDecoder(bytes.NewReader(raw))
		token, err := d.Token()
		if err != nil || token != json.Delim('[') {
			return ErrInvalid
		}
		for d.More() {
			var value json.RawMessage
			if d.Decode(&value) != nil || wireShape(value, t.Elem()) != nil {
				return ErrInvalid
			}
		}
		if token, err = d.Token(); err != nil || token != json.Delim(']') {
			return ErrInvalid
		}
		if _, err = d.Token(); err != io.EOF {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
}
func strict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > MaxJSONBytes || !validUnicode(raw) || wireShape(raw, reflect.TypeOf(out).Elem()) != nil || json.Unmarshal(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}
func DecodeSnapshot(raw []byte) (Snapshot, error) {
	var value Snapshot
	if strict(raw, &value) != nil || value.Validate() != nil {
		return Snapshot{}, ErrInvalid
	}
	return value, nil
}
func DecodeStatus(raw []byte) (Status, error) {
	var value Status
	if strict(raw, &value) != nil || value.Validate() != nil {
		return Status{}, ErrInvalid
	}
	return value, nil
}
func DecodeMutation(raw []byte) (Mutation, error) {
	var value Mutation
	if strict(raw, &value) != nil || value.Validate() != nil {
		return Mutation{}, ErrInvalid
	}
	return value, nil
}
func DecodeResult(raw []byte) (Result, error) {
	var value Result
	if strict(raw, &value) != nil || value.Validate() != nil {
		return Result{}, ErrInvalid
	}
	return value, nil
}
