package boundedjson

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

var errNumber = errors.New("invalid or out-of-range JSON number")

// Check PostgreSQL numeric bounds and expansion size before allocating output.
func normalizeNumber(raw string, budget int) (string, error) {
	if budget < 1 {
		return "", errLimit
	}
	if len(raw) == 0 || strings.TrimSpace(raw) != raw || !json.Valid([]byte(raw)) || raw[0] != '-' && (raw[0] < '0' || raw[0] > '9') {
		return "", errNumber
	}
	neg := raw[0] == '-'
	s := raw
	if neg {
		s = s[1:]
	}
	exp := int64(0)
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		var err error
		exp, err = strconv.ParseInt(s[i+1:], 10, 64)
		if err != nil || exp > 1000000 || exp < -1000000 {
			return "", errNumber
		}
		s = s[:i]
	}
	scale := int64(0)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		scale = int64(len(s) - i - 1)
		s = s[:i] + s[i+1:]
	}
	if scale-exp > 16383 {
		return "", errNumber
	}
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0", nil
	}
	decimal := int64(len(s)) - scale + exp
	if decimal > 131072 {
		return "", errNumber
	}
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	size := int64(len(s))
	switch {
	case decimal <= 0:
		size = 2 - decimal + int64(len(s))
	case decimal < int64(len(s)):
		size++
	case decimal > int64(len(s)):
		size = decimal
	}
	if neg {
		size++
	}
	if size > int64(budget) {
		return "", errLimit
	}
	var result string
	switch {
	case decimal <= 0:
		result = "0." + strings.Repeat("0", int(-decimal)) + s
	case decimal < int64(len(s)):
		result = s[:int(decimal)] + "." + s[int(decimal):]
	default:
		result = s + strings.Repeat("0", int(decimal)-len(s))
	}
	if neg {
		result = "-" + result
	}
	return result, nil
}
