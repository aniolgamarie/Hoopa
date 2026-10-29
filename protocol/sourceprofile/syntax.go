package sourceprofile

import (
	"encoding/json"
	"net/netip"
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

// readValue rejects duplicate keys and null at every depth, before Go's normal
// decoder can silently replace duplicates or convert null to a zero value.
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, ErrProfile
	}
	t, e := d.Token()
	if e != nil || t == nil {
		return nil, ErrProfile
	}
	delim, compound := t.(json.Delim)
	if !compound {
		return t, nil
	}
	switch delim {
	case '{':
		v := map[string]any{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return nil, ErrProfile
			}
			key, ok := k.(string)
			if !ok {
				return nil, ErrProfile
			}
			if _, ok = v[key]; ok {
				return nil, ErrProfile
			}
			child, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			v[key] = child
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, ErrProfile
		}
		return v, nil
	case '[':
		v := []any{}
		for d.More() {
			child, e := readValue(d, depth+1)
			if e != nil {
				return nil, e
			}
			v = append(v, child)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, ErrProfile
		}
		return v, nil
	}
	return nil, ErrProfile
}
func object(v any, required, optional string) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, k := range strings.Fields(required) {
		if !has(m, k) {
			return nil, false
		}
		allowed[k] = true
	}
	for _, k := range strings.Fields(optional) {
		allowed[k] = true
	}
	for k := range m {
		if !allowed[k] {
			return nil, false
		}
	}
	return m, true
}
func has(m map[string]any, k string) bool { _, ok := m[k]; return ok }
func token(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for i, c := range []byte(s) {
		alpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alpha && (i == 0 || !strings.ContainsRune("._:-", rune(c))) {
			return false
		}
	}
	return true
}
func id(s, prefix string) bool {
	return token(s) && strings.HasPrefix(s, prefix) && token(strings.TrimPrefix(s, prefix))
}
func credential(s string) bool {
	if len(s) < 32 || len(s) > 128 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 33 || c > 126 || c == ':' {
			return false
		}
	}
	return true
}
func domainToken(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range []byte(label) {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func atom(parts []string, entries map[string]bool) bool {
	if len(parts) < 2 || len(parts) > 3 || parts[1] == "" || strings.ContainsAny(parts[1], ",() \t\n\r\v\f") {
		return false
	}
	switch parts[0] {
	case "DOMAIN", "DOMAIN-SUFFIX":
		return len(parts) == 2 && domainToken(parts[1])
	case "IN-NAME":
		return len(parts) == 2 && entries[parts[1]]
	case "IP-CIDR":
		p, e := netip.ParsePrefix(parts[1])
		return e == nil && p.Addr().Is4() && (len(parts) == 2 || parts[2] == "no-resolve")
	}
	return false
}
func validRule(rule string, target func(string) bool, entries map[string]bool) bool {
	return validRuleWithAtom(rule, target, entries, atom)
}

func validRuleWithAtom(rule string, target func(string) bool, entries map[string]bool, checkAtom func([]string, map[string]bool) bool) bool {
	if strings.HasPrefix(rule, "AND,(") {
		// The grammar is AND,((atom),(atom)),target. Splitting at the
		// outer close keeps no-resolve inside an IP-CIDR atom intact.
		end := strings.LastIndex(rule, ")),")
		if end < 0 || !target(rule[end+3:]) {
			return false
		}
		if !strings.HasPrefix(rule, "AND,((") || end < len("AND,((") {
			return false
		}
		body := rule[len("AND,(("):end]
		atoms := strings.Split(body, "),(")
		if len(atoms) < 2 || len(atoms) > 8 {
			return false
		}
		for _, a := range atoms {
			if !checkAtom(strings.Split(a, ","), entries) {
				return false
			}
		}
		return true
	}
	parts := strings.Split(rule, ",")
	if len(parts) < 3 {
		return false
	}
	// no-resolve is the sole supported rule option and follows the target.
	if parts[0] == "IP-CIDR" && len(parts) == 4 {
		return target(parts[2]) && checkAtom([]string{parts[0], parts[1], parts[3]}, entries)
	}
	return len(parts) == 3 && target(parts[2]) && checkAtom(parts[:2], entries)
}

func boundedText(s string, max int, nonempty bool) bool {
	return utf8.ValidString(s) && len(s) <= max && (!nonempty || len(s) > 0)
}
