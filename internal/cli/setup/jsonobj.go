package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Object is a JSON object that keeps the order of its keys, so that editing settings.json does
// not shuffle the rest of the file. Other JSON values are decoded as string, json.Number (the
// number text is kept as is), bool, nil and []any.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject is an empty object.
func NewObject() *Object { return &Object{vals: map[string]any{}} }

// Get returns the value of key.
func (o *Object) Get(key string) (any, bool) {
	v, ok := o.vals[key]
	return v, ok
}

// Keys returns the keys in order.
func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

// with returns a copy of o with key set to value (in place if present, else last), or without
// the key when remove is true.
func (o *Object) with(key string, value any, remove bool) *Object {
	out := NewObject()
	found := false
	for _, k := range o.keys {
		if k == key {
			found = true
			if remove {
				continue
			}
			out.set(k, value)
		} else {
			out.set(k, o.vals[k])
		}
	}
	if !found && !remove {
		out.set(key, value)
	}
	return out
}

func (o *Object) set(key string, value any) {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
}

// ParseJSON decodes text, which must hold exactly one JSON value.
func ParseJSON(text string) (any, error) {
	if !json.Valid([]byte(text)) {
		var probe any
		err := json.Unmarshal([]byte(text), &probe)
		if err == nil {
			err = errors.New("invalid JSON")
		}
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	return decodeValue(dec)
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	if d == '[' {
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	obj := NewObject()
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, err
		}
		v, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}
		key, ok := k.(string)
		if !ok {
			return nil, errors.New("invalid JSON object key")
		}
		obj.set(key, v)
	}
	_, err = dec.Token()
	return obj, err
}

// marshalJSON is the JSON text of v like JSON.stringify(v, null, indent): no HTML escaping,
// "{}" and "[]" for empty containers. indent is at most ten characters.
func marshalJSON(v any, indent string) string {
	if len(indent) > 10 {
		indent = indent[:10]
	}
	var b bytes.Buffer
	writeValue(&b, v, indent, "")
	return b.String()
}

func writeValue(w io.StringWriter, v any, indent, cur string) {
	nl := func(c string) string {
		if indent == "" {
			return ""
		}
		return "\n" + c
	}
	inner := cur + indent
	switch x := v.(type) {
	case nil:
		_, _ = w.WriteString("null")
	case bool:
		_, _ = w.WriteString(strconv.FormatBool(x))
	case json.Number:
		_, _ = w.WriteString(x.String())
	case string:
		writeString(w, x)
	case []any:
		if len(x) == 0 {
			_, _ = w.WriteString("[]")
			return
		}
		_, _ = w.WriteString("[")
		for i, e := range x {
			if i > 0 {
				_, _ = w.WriteString(",")
			}
			_, _ = w.WriteString(nl(inner))
			writeValue(w, e, indent, inner)
		}
		_, _ = w.WriteString(nl(cur) + "]")
	case *Object:
		if len(x.keys) == 0 {
			_, _ = w.WriteString("{}")
			return
		}
		_, _ = w.WriteString("{")
		for i, k := range x.keys {
			if i > 0 {
				_, _ = w.WriteString(",")
			}
			_, _ = w.WriteString(nl(inner))
			writeString(w, k)
			_, _ = w.WriteString(":")
			if indent != "" {
				_, _ = w.WriteString(" ")
			}
			writeValue(w, x.vals[k], indent, inner)
		}
		_, _ = w.WriteString(nl(cur) + "}")
	default:
		// Unreachable: values only come from decodeValue and the plan builders. Returning an
		// error would leak into every caller of marshalJSON/FormatJSON for a programming bug.
		panic(fmt.Sprintf("setup: unsupported JSON value %T", v))
	}
}

func writeString(w io.StringWriter, s string) {
	_, _ = w.WriteString(`"`)
	for _, r := range s {
		switch {
		case r == '"':
			_, _ = w.WriteString(`\"`)
		case r == '\\':
			_, _ = w.WriteString(`\\`)
		case r == '\b':
			_, _ = w.WriteString(`\b`)
		case r == '\f':
			_, _ = w.WriteString(`\f`)
		case r == '\n':
			_, _ = w.WriteString(`\n`)
		case r == '\r':
			_, _ = w.WriteString(`\r`)
		case r == '\t':
			_, _ = w.WriteString(`\t`)
		case r < 0x20:
			_, _ = w.WriteString(fmt.Sprintf(`\u%04x`, r))
		default:
			_, _ = w.WriteString(string(r))
		}
	}
	_, _ = w.WriteString(`"`)
}

var indentRE = regexp.MustCompile(`(?m)^([ \t]+)\S`)

// DetectIndent is the indentation of a JSON text (the first indented line), two spaces by
// default.
func DetectIndent(text string) string {
	if m := indentRE.FindStringSubmatch(text); m != nil {
		return m[1]
	}
	return "  "
}

// FormatJSON is value as JSON text in the style of previous (indentation, final newline).
// previous is nil when there was no text.
func FormatJSON(value any, previous *string) string {
	finalNewline := previous == nil || strings.HasSuffix(*previous, "\n")
	prev := ""
	if previous != nil {
		prev = *previous
	}
	text := marshalJSON(value, DetectIndent(prev))
	if finalNewline {
		return text + "\n"
	}
	return text
}
