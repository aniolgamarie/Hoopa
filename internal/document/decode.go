// Package document decodes bounded, strict JSON/YAML values without performing IO.
package document

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

var ErrInvalid = errors.New("DOCUMENT_INVALID")
var ErrLimit = errors.New("DOCUMENT_LIMIT")

// MaxNumberLiteralBytes caps each JSON or YAML numeric literal, even when a
// caller raises MaxBytes. This bounds numeric normalization work independently
// of the document size. Longer literals return ErrLimit; strings are unaffected.
const MaxNumberLiteralBytes = 128

type Limits struct{ MaxBytes, MaxDepth, MaxAliases, MaxValues int }

// Decode returns maps with string keys, slices, strings, bools, nil and
// json.Number. Zero limits select defaults; negative limits are invalid.
// Alias expansion is charged to both depth and value budgets.
func Decode(raw []byte, limits Limits) (any, error) {
	defaults := Limits{128 << 10, 64, 64, 200000}
	fields := []*int{&limits.MaxBytes, &limits.MaxDepth, &limits.MaxAliases, &limits.MaxValues}
	defs := []int{defaults.MaxBytes, defaults.MaxDepth, defaults.MaxAliases, defaults.MaxValues}
	for i, p := range fields {
		if *p < 0 {
			return nil, ErrLimit
		}
		if *p == 0 {
			*p = defs[i]
		}
	}
	if len(raw) > limits.MaxBytes {
		return nil, ErrLimit
	}
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	if json.Valid(raw) {
		if !validJSONUnicode(raw) {
			return nil, ErrInvalid
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		w := walker{limits: limits}
		return w.jsonValue(d, 0)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var root, extra yaml.Node
	if decoder.Decode(&root) != nil || len(root.Content) != 1 {
		return nil, ErrInvalid
	}
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	w := walker{limits: limits, active: map[*yaml.Node]bool{}}
	return w.value(root.Content[0], 0)
}

type walker struct {
	limits          Limits
	aliases, values int
	active          map[*yaml.Node]bool
}

func (w *walker) value(n *yaml.Node, depth int) (any, error) {
	w.values++
	if w.values > w.limits.MaxValues || depth > w.limits.MaxDepth {
		return nil, ErrLimit
	}
	if n == nil || w.active[n] {
		return nil, ErrInvalid
	}
	w.active[n] = true
	defer delete(w.active, n)
	if n.Kind == yaml.AliasNode {
		w.aliases++
		if w.aliases > w.limits.MaxAliases {
			return nil, ErrLimit
		}
		return w.value(n.Alias, depth)
	}
	switch n.Kind {
	case yaml.MappingNode:
		if depth >= w.limits.MaxDepth {
			return nil, ErrLimit
		}
		if n.ShortTag() != "!!map" || len(n.Content)%2 != 0 {
			return nil, ErrInvalid
		}
		out := make(map[string]any, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.ShortTag() != "!!str" || key.Value == "<<" {
				return nil, ErrInvalid
			}
			if _, ok := out[key.Value]; ok {
				return nil, ErrInvalid
			}
			// Keys also count against the expanded value budget.
			if _, err := w.value(key, depth+1); err != nil {
				return nil, err
			}
			v, err := w.value(n.Content[i+1], depth+1)
			if err != nil {
				return nil, err
			}
			out[key.Value] = v
		}
		return out, nil
	case yaml.SequenceNode:
		if depth >= w.limits.MaxDepth {
			return nil, ErrLimit
		}
		if n.ShortTag() != "!!seq" {
			return nil, ErrInvalid
		}
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			v, err := w.value(c, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case yaml.ScalarNode:
		switch n.ShortTag() {
		case "!!str":
			return n.Value, nil
		case "!!null":
			if n.Value == "" || n.Value == "~" || strings.EqualFold(n.Value, "null") {
				return nil, nil
			}
		case "!!bool":
			if n.Value == "true" || n.Value == "True" || n.Value == "TRUE" {
				return true, nil
			}
			if n.Value == "false" || n.Value == "False" || n.Value == "FALSE" {
				return false, nil
			}
		case "!!int":
			if len(n.Value) > MaxNumberLiteralBytes {
				return nil, ErrLimit
			}
			s := strings.ReplaceAll(n.Value, "_", "")
			base := 0
			if decimalInt.MatchString(s) {
				base = 10
			}
			v, ok := new(big.Int).SetString(s, base)
			if ok {
				return json.Number(v.String()), nil
			}
		case "!!float":
			if len(n.Value) > MaxNumberLiteralBytes {
				return nil, ErrLimit
			}
			s := strings.ReplaceAll(n.Value, "_", "")
			s = strings.TrimPrefix(s, "+")
			if strings.HasPrefix(s, ".") {
				s = "0" + s
			}
			if strings.HasPrefix(s, "-.") {
				s = "-0" + s[1:]
			}
			if i := strings.IndexAny(s, "eE"); i >= 0 && i > 0 && s[i-1] == '.' {
				s = s[:i] + "0" + s[i:]
			} else if strings.HasSuffix(s, ".") {
				s += "0"
			}
			if jsonNumber.MatchString(s) {
				return json.Number(s), nil
			}
		}
	}
	return nil, ErrInvalid
}

var decimalInt = regexp.MustCompile(`^[+-]?[0-9]+$`)
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// encoding/json accepts isolated surrogate escapes with replacement. Validate
// those escapes before decoding so source text is never silently repaired.
func validJSONUnicode(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
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
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func (w *walker) jsonValue(d *json.Decoder, depth int) (any, error) {
	w.values++
	if w.values > w.limits.MaxValues || depth > w.limits.MaxDepth {
		return nil, ErrLimit
	}
	token, err := d.Token()
	if err != nil {
		return nil, ErrInvalid
	}
	if number, ok := token.(json.Number); ok && len(number.String()) > MaxNumberLiteralBytes {
		return nil, ErrLimit
	}
	delim, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	if depth >= w.limits.MaxDepth {
		return nil, ErrLimit
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, ErrInvalid
			}
			s, ok := key.(string)
			if !ok {
				return nil, ErrInvalid
			}
			if _, exists := out[s]; exists {
				return nil, ErrInvalid
			}
			w.values++
			if w.values > w.limits.MaxValues {
				return nil, ErrLimit
			}
			value, err := w.jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out[s] = value
		}
		if _, err := d.Token(); err != nil {
			return nil, ErrInvalid
		}
		return out, nil
	case '[':
		out := []any{}
		for d.More() {
			value, err := w.jsonValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		if _, err := d.Token(); err != nil {
			return nil, ErrInvalid
		}
		return out, nil
	}
	return nil, ErrInvalid
}
