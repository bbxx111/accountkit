package idp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const maxAlipayBody = 64 << 10

var errAlipayJSON = errors.New("idp: invalid alipay response structure")

// alipayObject retains original value bytes. Validate all nested objects before
// decoding so duplicate keys (including escaped aliases) cannot be hidden.
func alipayObject(raw []byte) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > maxAlipayBody || !utf8.Valid(raw) {
		return nil, errAlipayJSON
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := alipayJSONValue(d, 0); err != nil {
		return nil, errAlipayJSON
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, errAlipayJSON
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, errAlipayJSON
	}
	return object, nil
}

func alipayJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errAlipayJSON
	}
	token, err := d.Token()
	if err != nil {
		return errAlipayJSON
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return errAlipayJSON
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return errAlipayJSON
			}
			seen[key] = true
			if err := alipayJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return errAlipayJSON
		}
	case '[':
		for d.More() {
			if err := alipayJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return errAlipayJSON
		}
	default:
		return errAlipayJSON
	}
	return nil
}

func alipayString(object map[string]json.RawMessage, key string) (string, error) {
	raw, exists := object[key]
	if !exists {
		return "", nil
	}
	var result string
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &result) != nil {
		return "", errAlipayJSON
	}
	return result, nil
}
