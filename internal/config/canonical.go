package config

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxCanonicalBytes limits the original input, including whitespace. Canonical
// output may grow through bounded decimal expansion; callers separately enforce
// their final record/request size limits.
const MaxCanonicalBytes = 128 << 10

// MaxCanonicalDepth is the maximum number of simultaneously open JSON objects
// and arrays. A scalar does not introduce a container level.
const MaxCanonicalDepth = 64

// ErrCanonicalJSON is deliberately constant and never includes input material.
var ErrCanonicalJSON = errors.New("CANONICAL_JSON_INVALID")

// CanonicalJSON implements RFC 8785 serialization with Hoopa's additional
// restriction on integral float64 values outside [-2^53+1, 2^53-1]. Strings must
// contain valid Unicode scalars and are preserved without normalization. Parsing
// is bounded before allocation/recursion; no permissive JSON decoder can replace
// invalid Unicode or silently collapse duplicate object keys.
func CanonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > MaxCanonicalBytes || !utf8.Valid(raw) {
		return nil, ErrCanonicalJSON
	}
	p := canonicalParser{raw: raw}
	result, ok := p.value(0)
	p.space()
	if !ok || p.pos != len(raw) {
		return nil, ErrCanonicalJSON
	}
	return result, nil
}

type canonicalParser struct {
	raw []byte
	pos int
}
type canonicalMember struct {
	key   string
	units []uint16
	value []byte
}

func (p *canonicalParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\r', '\n':
			p.pos++
		default:
			return
		}
	}
}
func (p *canonicalParser) take(b byte) bool {
	if p.pos < len(p.raw) && p.raw[p.pos] == b {
		p.pos++
		return true
	}
	return false
}
func (p *canonicalParser) value(depth int) ([]byte, bool) {
	p.space()
	if p.pos == len(p.raw) {
		return nil, false
	}
	switch p.raw[p.pos] {
	case '{':
		if depth >= MaxCanonicalDepth {
			return nil, false
		}
		return p.object(depth + 1)
	case '[':
		if depth >= MaxCanonicalDepth {
			return nil, false
		}
		return p.array(depth + 1)
	case '"':
		text, ok := p.text()
		if !ok {
			return nil, false
		}
		return appendCanonicalString(nil, text), true
	case 'n':
		return p.literal("null")
	case 't':
		return p.literal("true")
	case 'f':
		return p.literal("false")
	default:
		return p.number()
	}
}
func (p *canonicalParser) literal(s string) ([]byte, bool) {
	if len(p.raw)-p.pos < len(s) || string(p.raw[p.pos:p.pos+len(s)]) != s {
		return nil, false
	}
	p.pos += len(s)
	return []byte(s), true
}
func (p *canonicalParser) array(depth int) ([]byte, bool) {
	p.pos++
	p.space()
	out := []byte{'['}
	if p.take(']') {
		return append(out, ']'), true
	}
	for {
		item, ok := p.value(depth)
		if !ok {
			return nil, false
		}
		out = append(out, item...)
		p.space()
		if p.take(']') {
			return append(out, ']'), true
		}
		if !p.take(',') {
			return nil, false
		}
		out = append(out, ',')
	}
}
func (p *canonicalParser) object(depth int) ([]byte, bool) {
	p.pos++
	p.space()
	if p.take('}') {
		return []byte("{}"), true
	}
	members := []canonicalMember{}
	seen := map[string]struct{}{}
	for {
		p.space()
		key, ok := p.text()
		if !ok {
			return nil, false
		}
		if _, exists := seen[key]; exists {
			return nil, false
		}
		seen[key] = struct{}{}
		p.space()
		if !p.take(':') {
			return nil, false
		}
		value, ok := p.value(depth)
		if !ok {
			return nil, false
		}
		members = append(members, canonicalMember{key: key, units: utf16.Encode([]rune(key)), value: value})
		p.space()
		if p.take('}') {
			break
		}
		if !p.take(',') {
			return nil, false
		}
	}
	sort.Slice(members, func(i, j int) bool {
		a, b := members[i].units, members[j].units
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	out := []byte{'{'}
	for i, m := range members {
		if i != 0 {
			out = append(out, ',')
		}
		out = appendCanonicalString(out, m.key)
		out = append(out, ':')
		out = append(out, m.value...)
	}
	return append(out, '}'), true
}
func (p *canonicalParser) hex4() (uint16, bool) {
	if len(p.raw)-p.pos < 4 {
		return 0, false
	}
	var value uint16
	for _, b := range p.raw[p.pos : p.pos+4] {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value |= uint16(b - '0')
		case b >= 'a' && b <= 'f':
			value |= uint16(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			value |= uint16(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	p.pos += 4
	return value, true
}
func (p *canonicalParser) text() (string, bool) {
	if !p.take('"') {
		return "", false
	}
	out := []byte{}
	for p.pos < len(p.raw) {
		b := p.raw[p.pos]
		p.pos++
		if b == '"' {
			return string(out), true
		}
		if b < 0x20 {
			return "", false
		}
		if b != '\\' {
			out = append(out, b)
			continue
		}
		if p.pos == len(p.raw) {
			return "", false
		}
		escape := p.raw[p.pos]
		p.pos++
		switch escape {
		case '"', '\\', '/':
			out = append(out, escape)
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			unit, ok := p.hex4()
			if !ok {
				return "", false
			}
			r := rune(unit)
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if !p.take('\\') || !p.take('u') {
					return "", false
				}
				low, ok := p.hex4()
				if !ok || low < 0xdc00 || low > 0xdfff {
					return "", false
				}
				r = utf16.DecodeRune(r, rune(low))
			case unit >= 0xdc00 && unit <= 0xdfff:
				return "", false
			}
			out = utf8.AppendRune(out, r)
		default:
			return "", false
		}
	}
	return "", false
}
func appendCanonicalString(out []byte, s string) []byte {
	const hex = "0123456789abcdef"
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		b := s[i]
		switch b {
		case '"', '\\':
			out = append(out, '\\', b)
		case '\b':
			out = append(out, '\\', 'b')
		case '\t':
			out = append(out, '\\', 't')
		case '\n':
			out = append(out, '\\', 'n')
		case '\f':
			out = append(out, '\\', 'f')
		case '\r':
			out = append(out, '\\', 'r')
		default:
			if b < 0x20 {
				out = append(out, '\\', 'u', '0', '0', hex[b>>4], hex[b&15])
			} else {
				out = append(out, b)
			}
		}
	}
	return append(out, '"')
}
func canonicalDigit(b byte) bool { return b >= '0' && b <= '9' }
func (p *canonicalParser) number() ([]byte, bool) {
	start := p.pos
	p.take('-')
	if p.pos == len(p.raw) {
		return nil, false
	}
	if !p.take('0') {
		if p.raw[p.pos] < '1' || p.raw[p.pos] > '9' {
			return nil, false
		}
		for p.pos < len(p.raw) && canonicalDigit(p.raw[p.pos]) {
			p.pos++
		}
	}
	if p.take('.') {
		first := p.pos
		for p.pos < len(p.raw) && canonicalDigit(p.raw[p.pos]) {
			p.pos++
		}
		if p.pos == first {
			return nil, false
		}
	}
	if p.take('e') || p.take('E') {
		if !p.take('+') {
			p.take('-')
		}
		first := p.pos
		for p.pos < len(p.raw) && canonicalDigit(p.raw[p.pos]) {
			p.pos++
		}
		if p.pos == first {
			return nil, false
		}
	}
	value, err := strconv.ParseFloat(string(p.raw[start:p.pos]), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || math.Trunc(value) == value && math.Abs(value) > 9007199254740991 {
		return nil, false
	}
	if value == 0 {
		return []byte("0"), true
	} // Includes negative zero and underflow.
	// Go's shortest-roundtrip conversion uses Ryu. Select the ECMAScript fixed
	// notation interval and remove the exponent padding that strconv emits.
	format := byte('f')
	absolute := math.Abs(value)
	if absolute < 1e-6 || absolute >= 1e21 {
		format = 'e'
	}
	text := strconv.FormatFloat(value, format, -1, 64)
	if index := strings.IndexByte(text, 'e'); index >= 0 && index+3 < len(text) && text[index+2] == '0' {
		text = text[:index+2] + text[index+3:]
	}
	return []byte(text), true
}
