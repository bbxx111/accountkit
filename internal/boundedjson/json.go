// Package boundedjson parses bounded, exact JSON suitable for PostgreSQL JSONB.
package boundedjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits includes the root and containers in depth and value node counts.
// MaxBytes and MaxDepth must be positive. MaxNodes zero disables the node cap.
type Limits struct{ MaxBytes, MaxDepth, MaxNodes int }

var (
	errSyntax  = errors.New("invalid JSON syntax")
	errUnicode = errors.New("invalid JSON Unicode")
	errLimit   = errors.New("JSON resource limit exceeded")
	errType    = errors.New("unsupported JSON value type")
)

// Decode rejects duplicate decoded keys, trailing documents, invalid Unicode,
// NUL, and numbers outside PostgreSQL numeric limits or the output byte budget.
// It preserves null and represents numbers exactly as normalized json.Number.
func Decode(raw []byte, limits Limits) (any, error) {
	if limits.MaxBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxNodes < 0 || len(raw) > limits.MaxBytes {
		return nil, errLimit
	}
	if err := checkUnicode(raw); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	nodes := 0
	// Reserve canonical bytes as values are parsed, including punctuation. In
	// particular, a number receives only the remaining document budget before
	// normalizeNumber allocates its decimal expansion.
	remaining := limits.MaxBytes
	consume := func(n int) error {
		if n > remaining {
			return errLimit
		}
		remaining -= n
		return nil
	}
	consumeString := func(s string) error {
		n, err := encodedStringSize(s, remaining)
		if err != nil {
			return err
		}
		return consume(n)
	}
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		if depth > limits.MaxDepth || depth > 128 {
			return nil, errLimit
		}
		nodes++
		if limits.MaxNodes > 0 && nodes > limits.MaxNodes {
			return nil, errLimit
		}
		tok, err := d.Token()
		if err != nil {
			return nil, errSyntax
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{':
				if err := consume(2); err != nil {
					return nil, err
				}
				obj := make(map[string]any)
				for d.More() {
					if len(obj) > 0 {
						if err := consume(1); err != nil {
							return nil, err
						}
					}
					key, err := d.Token()
					if err != nil {
						return nil, errSyntax
					}
					s, ok := key.(string)
					if !ok {
						return nil, errSyntax
					}
					if _, exists := obj[s]; exists {
						return nil, errors.New("duplicate JSON object key")
					}
					if err := consumeString(s); err != nil {
						return nil, err
					}
					if err := consume(1); err != nil {
						return nil, err
					}
					value, err := parse(depth + 1)
					if err != nil {
						return nil, err
					}
					obj[s] = value
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return nil, errSyntax
				}
				return obj, nil
			case '[':
				if err := consume(2); err != nil {
					return nil, err
				}
				arr := make([]any, 0)
				for d.More() {
					if len(arr) > 0 {
						if err := consume(1); err != nil {
							return nil, err
						}
					}
					value, err := parse(depth + 1)
					if err != nil {
						return nil, err
					}
					arr = append(arr, value)
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return nil, errSyntax
				}
				return arr, nil
			default:
				return nil, errSyntax
			}
		case json.Number:
			n, err := normalizeNumber(string(v), remaining)
			if err != nil {
				return nil, err
			}
			if err := consume(len(n)); err != nil {
				return nil, err
			}
			return json.Number(n), nil
		case string:
			if err := consumeString(v); err != nil {
				return nil, err
			}
			return v, nil
		case bool:
			n := 5
			if v {
				n = 4
			}
			if err := consume(n); err != nil {
				return nil, err
			}
			return v, nil
		case nil:
			if err := consume(4); err != nil {
				return nil, err
			}
			return nil, nil
		default:
			return nil, errSyntax
		}
	}
	v, err := parse(1)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errSyntax
	}
	return v, nil
}

// encodedStringSize matches encoding/json with EscapeHTML disabled, including
// its mandatory U+2028/U+2029 escapes. It checks incrementally without allocating
// an encoded string, so keys and string values share the same bounded budget.
func encodedStringSize(s string, budget int) (int, error) {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return 0, errUnicode
	}
	n := 2
	if n > budget {
		return 0, errLimit
	}
	for _, r := range s {
		size := utf8.RuneLen(r)
		switch r {
		case '"', '\\', '\b', '\f', '\n', '\r', '\t':
			size = 2
		case '\u2028', '\u2029':
			size = 6
		default:
			if r < 0x20 {
				size = 6
			}
		}
		if size > budget-n {
			return 0, errLimit
		}
		n += size
	}
	return n, nil
}

// checkUnicode checks escapes before encoding/json can replace lone surrogates.
func checkUnicode(raw []byte) error {
	if !utf8.Valid(raw) {
		return errUnicode
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for ; i < len(raw) && raw[i] != '"'; i++ {
			if raw[i] == 0 {
				return errUnicode
			}
			if raw[i] != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return errSyntax
			}
			if raw[i] != 'u' {
				continue
			}
			u, ok := hexUnit(raw, i+1)
			if !ok {
				return errSyntax
			}
			i += 4
			if u == 0 || u >= 0xdc00 && u <= 0xdfff {
				return errUnicode
			}
			if u >= 0xd800 && u <= 0xdbff {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return errUnicode
				}
				low, ok := hexUnit(raw, i+3)
				if !ok || low < 0xdc00 || low > 0xdfff {
					return errUnicode
				}
				i += 6
			}
		}
	}
	return nil
}
func hexUnit(raw []byte, start int) (uint64, bool) {
	if start+4 > len(raw) {
		return 0, false
	}
	n, err := strconv.ParseUint(string(raw[start:start+4]), 16, 16)
	return n, err == nil
}

// Canonical sorts keys and normalizes numbers. Its generic output cap is 64 KiB;
// profile callers must additionally enforce their 16 KiB result cap. Unsupported
// types, cycles, excessive depth, and invalid strings return fixed errors.
func Canonical(value any) ([]byte, error) { return canonical(value, 64*1024) }
func canonical(value any, budget int) ([]byte, error) {
	out := make([]byte, 0)
	appendBytes := func(b []byte) error {
		if len(b) > budget-len(out) {
			return errLimit
		}
		out = append(out, b...)
		return nil
	}
	stringBytes := func(s string) ([]byte, error) {
		if _, err := encodedStringSize(s, budget-len(out)); err != nil {
			return nil, err
		}
		var b bytes.Buffer
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		if err := e.Encode(s); err != nil {
			return nil, errType
		}
		return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
	}
	var write func(any, int) error
	write = func(v any, depth int) error {
		if depth > 128 {
			return errLimit
		}
		switch x := v.(type) {
		case nil:
			return appendBytes([]byte("null"))
		case bool:
			if x {
				return appendBytes([]byte("true"))
			}
			return appendBytes([]byte("false"))
		case string:
			b, err := stringBytes(x)
			if err != nil {
				return err
			}
			return appendBytes(b)
		case json.Number:
			n, err := normalizeNumber(string(x), budget-len(out))
			if err != nil {
				return err
			}
			return appendBytes([]byte(n))
		case []any:
			if len(x) > (budget-len(out))/2 {
				return errLimit
			}
			if err := appendBytes([]byte{'['}); err != nil {
				return err
			}
			for i, e := range x {
				if i > 0 {
					if err := appendBytes([]byte{','}); err != nil {
						return err
					}
				}
				if err := write(e, depth+1); err != nil {
					return err
				}
			}
			return appendBytes([]byte{']'})
		case map[string]any:
			if len(x) > (budget-len(out))/4 {
				return errLimit
			}
			if err := appendBytes([]byte{'{'}); err != nil {
				return err
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for i, k := range keys {
				if i > 0 {
					if err := appendBytes([]byte{','}); err != nil {
						return err
					}
				}
				b, err := stringBytes(k)
				if err != nil {
					return err
				}
				if err := appendBytes(b); err != nil {
					return err
				}
				if err := appendBytes([]byte{':'}); err != nil {
					return err
				}
				if err := write(x[k], depth+1); err != nil {
					return err
				}
			}
			return appendBytes([]byte{'}'})
		default:
			return errType
		}
	}
	if err := write(value, 1); err != nil {
		return nil, err
	}
	return out, nil
}

// Clone deeply copies a valid, acyclic tree returned by Decode.
func Clone(value any) any {
	switch x := value.(type) {
	case map[string]any:
		r := make(map[string]any, len(x))
		for k, v := range x {
			r[k] = Clone(v)
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = Clone(v)
		}
		return r
	default:
		return value
	}
}
