package routingcandidate

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

type providerSnapshot struct {
	Schema     string         `json:"schema"`
	ProviderID string         `json:"provider_id"`
	Revision   string         `json:"revision"`
	Nodes      []providerNode `json:"nodes"`
	hash       string
}
type providerNode struct {
	NodeID      string `json:"node_id"`
	DisplayName string `json:"display_name"`
	Region      string `json:"region"`
	Type        string `json:"type"`
	Server      string `json:"server"`
	Port        uint16 `json:"port"`
}

func decodeSnapshot(raw []byte) (providerSnapshot, error) {
	bad := func() (providerSnapshot, error) { return providerSnapshot{}, ErrCandidate }
	if len(raw) == 0 || len(raw) > 65536 || !validUnicodeJSON(raw) {
		return bad()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	value, e := readStrict(d, 0)
	if e != nil {
		return bad()
	}
	if _, e = d.Token(); e != io.EOF {
		return bad()
	}
	root, ok := exactObject(value, "schema", "provider_id", "revision", "nodes")
	if !ok {
		return bad()
	}
	nodes, ok := root["nodes"].([]any)
	if !ok || len(nodes) < 1 || len(nodes) > 64 {
		return bad()
	}
	for _, node := range nodes {
		if _, ok := exactObject(node, "node_id", "display_name", "region", "type", "server", "port"); !ok {
			return bad()
		}
	}
	var p providerSnapshot
	if json.Unmarshal(raw, &p) != nil || p.Schema != "hoopa-routing-provider-fixture-v1" || !token(p.ProviderID) || !token(p.Revision) {
		return bad()
	}
	seen := map[string]bool{}
	for _, node := range p.Nodes {
		if !token(node.NodeID) || seen[node.NodeID] || !alias(node.DisplayName) || !token(node.Region) || node.Type != "http" && node.Type != "socks5" || node.Server != "127.0.0.1" || node.Port == 0 {
			return bad()
		}
		seen[node.NodeID] = true
	}
	p.hash = digest(raw)
	return p, nil
}
func exactObject(value any, fields ...string) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	if !ok || len(m) != len(fields) {
		return nil, false
	}
	for _, field := range fields {
		if _, ok := m[field]; !ok {
			return nil, false
		}
	}
	return m, true
}

// readStrict rejects duplicate keys and null before ordinary decoding can
// replace them. A depth bound also applies to values in rejected unknown fields.
func readStrict(d *json.Decoder, depth int) (any, error) {
	if depth > 8 {
		return nil, ErrCandidate
	}
	value, e := d.Token()
	if e != nil || value == nil {
		return nil, ErrCandidate
	}
	delimiter, compound := value.(json.Delim)
	if !compound {
		return value, nil
	}
	switch delimiter {
	case '{':
		result := map[string]any{}
		for d.More() {
			key, e := d.Token()
			if e != nil {
				return nil, ErrCandidate
			}
			name, ok := key.(string)
			if !ok {
				return nil, ErrCandidate
			}
			if _, exists := result[name]; exists {
				return nil, ErrCandidate
			}
			child, e := readStrict(d, depth+1)
			if e != nil {
				return nil, e
			}
			result[name] = child
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, ErrCandidate
		}
		return result, nil
	case '[':
		result := []any{}
		for d.More() {
			child, e := readStrict(d, depth+1)
			if e != nil {
				return nil, e
			}
			result = append(result, child)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, ErrCandidate
		}
		return result, nil
	}
	return nil, ErrCandidate
}

// encoding/json silently repairs lone surrogate escapes. Check them before
// decoding, while allowing a literal escaped backslash followed by uXXXX.
func validUnicodeJSON(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		value, e := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if e != nil || value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+12 > len(raw) || string(raw[i+6:i+8]) != "\\u" {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 12
		} else {
			i += 6
		}
	}
	return true
}
