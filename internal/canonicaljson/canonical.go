// Package canonicaljson implements RFC 8785 for immutable request bindings.
// It rejects duplicate names, invalid Unicode and non-finite IEEE 754 numbers.
package canonicaljson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

func Canonicalize(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || !validSurrogates(raw) {
		return nil, fmt.Errorf("invalid Unicode")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := parse(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON")
	}
	return appendValue(nil, v), nil
}
func parse(d *json.Decoder, depth int) (any, error) {
	if depth > 100 {
		return nil, fmt.Errorf("JSON nesting limit")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch v := t.(type) {
	case json.Delim:
		if v == '{' {
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				name, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("invalid key")
				}
				if _, ok = m[name]; ok {
					return nil, fmt.Errorf("duplicate key")
				}
				m[name], e = parse(d, depth+1)
				if e != nil {
					return nil, e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid object")
			}
			return m, nil
		}
		if v == '[' {
			a := []any{}
			for d.More() {
				x, e := parse(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, x)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid array")
			}
			return a, nil
		}
		return nil, fmt.Errorf("invalid delimiter")
	case json.Number:
		n, e := strconv.ParseFloat(string(v), 64)
		if e != nil || math.IsInf(n, 0) || math.IsNaN(n) {
			return nil, fmt.Errorf("invalid number")
		}
		return n, nil
	default:
		return t, nil
	}
}
func appendValue(b []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(b, "null"...)
	case bool:
		return strconv.AppendBool(b, x)
	case string:
		return appendString(b, x)
	case float64:
		if x == 0 {
			return append(b, '0')
		}
		format := byte('f')
		if math.Abs(x) < 1e-6 || math.Abs(x) >= 1e21 {
			format = 'e'
		}
		s := strconv.FormatFloat(x, format, -1, 64)
		s = strings.ReplaceAll(strings.ReplaceAll(s, "e-0", "e-"), "e+0", "e+")
		return append(b, s...)
	case []any:
		b = append(b, '[')
		for i, v := range x {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendValue(b, v)
		}
		return append(b, ']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			a, c := utf16.Encode([]rune(keys[i])), utf16.Encode([]rune(keys[j]))
			for k := 0; k < len(a) && k < len(c); k++ {
				if a[k] != c[k] {
					return a[k] < c[k]
				}
			}
			return len(a) < len(c)
		})
		b = append(b, '{')
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendString(b, k)
			b = append(b, ':')
			b = appendValue(b, x[k])
		}
		return append(b, '}')
	default:
		panic("canonicaljson: unexpected parsed type")
	}
}
func appendString(b []byte, s string) []byte {
	const hex = "0123456789abcdef"
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', byte(r))
		case '\b':
			b = append(b, `\b`...)
		case '\t':
			b = append(b, `\t`...)
		case '\n':
			b = append(b, `\n`...)
		case '\f':
			b = append(b, `\f`...)
		case '\r':
			b = append(b, `\r`...)
		default:
			if r < 32 {
				b = append(b, '\\', 'u', '0', '0', hex[r>>4], hex[r&15])
			} else {
				b = utf8.AppendRune(b, r)
			}
		}
	}
	return append(b, '"')
}

// encoding/json replaces unpaired UTF-16 escapes with U+FFFD. Reject them
// before decoding; an actual U+FFFD and an escaped literal backslash are valid.
func validSurrogates(raw []byte) bool {
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
		v, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			w, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || w < 0xdc00 || w > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
