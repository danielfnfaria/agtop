package config

import (
	"strings"

	"github.com/pelletier/go-toml/v2/unstable"
)

// keyOrder records the keys of each table in the order they appear in the document, which a
// decoded map does not keep. Tables are addressed by their dotted path ("" is the root).
type keyOrder map[string][]string

func (o keyOrder) add(path []string, key string) {
	p := strings.Join(path, "\x00")
	for _, k := range o[p] {
		if k == key {
			return
		}
	}
	o[p] = append(o[p], key)
}

// keys returns the keys of m at path in document order, then any not seen by the parser.
func (o keyOrder) keys(path string, m map[string]any) []string {
	out := make([]string, 0, len(m))
	seen := make(map[string]bool, len(m))
	for _, k := range o[path] {
		if _, ok := m[k]; ok && !seen[k] {
			out = append(out, k)
			seen[k] = true
		}
	}
	for k := range m {
		if !seen[k] {
			out = append(out, k)
		}
	}
	return out
}

func keyParts(n *unstable.Node) []string {
	var parts []string
	for it := n.Key(); it.Next(); {
		parts = append(parts, string(it.Node().Data))
	}
	return parts
}

// orderKeys parses text once more, only to learn the key order. The text is already known to
// be valid TOML, so a parser error just yields what was seen so far.
func orderKeys(text string) keyOrder {
	o := keyOrder{}
	var p unstable.Parser
	p.Reset([]byte(text))
	var table []string
	for p.NextExpression() {
		n := p.Expression()
		switch n.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = keyParts(n)
			for i := range table {
				o.add(table[:i], table[i])
			}
		case unstable.KeyValue:
			o.addKeyValue(table, n)
		}
	}
	return o
}

func (o keyOrder) addKeyValue(table []string, n *unstable.Node) {
	parts := keyParts(n)
	path := append(append([]string(nil), table...), parts...)
	for i := range parts {
		o.add(path[:len(table)+i], parts[i])
	}
	if v := n.Value(); v != nil && v.Kind == unstable.InlineTable {
		for it := v.Children(); it.Next(); {
			o.addKeyValue(path, it.Node())
		}
	}
}
