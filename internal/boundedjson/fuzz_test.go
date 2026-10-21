package boundedjson

import (
	"bytes"
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
)

func FuzzDecodeCanonical(f *testing.F) {
	for _, seed := range []string{`null`, `{"n":9007199254740993}`, `{"n":1.00e-8}`, `{"a":[true,"\ud83d\ude00"]}`, `{"x":1,"x":2}`, `1e999999999`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		v, err := Decode(raw, profileLimits)
		if err != nil {
			return
		}
		b, err := Canonical(v)
		if err != nil {
			t.Fatal(err)
		}
		v2, err := Decode(b, profileLimits)
		if err != nil {
			t.Fatal(err)
		}
		b2, err := Canonical(v2)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != string(b2) {
			t.Fatal("unstable exact roundtrip")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var original any
		if err := decoder.Decode(&original); err != nil {
			t.Fatal(err)
		}
		if !exactValuesEqual(original, v2) {
			t.Fatal("roundtrip changed exact source value")
		}
	})
}

func exactValuesEqual(a, b any) bool {
	switch x := a.(type) {
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return false
		}
		r, ok := new(big.Rat).SetString(string(x))
		if !ok {
			return false
		}
		s, ok := new(big.Rat).SetString(string(y))
		return ok && r.Cmp(s) == 0
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			other, ok := y[k]
			if !ok || !exactValuesEqual(v, other) {
				return false
			}
		}
		return true
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i, v := range x {
			if !exactValuesEqual(v, y[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}
