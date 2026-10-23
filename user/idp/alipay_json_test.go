package idp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAlipayJSONStructure(t *testing.T) {
	for _, raw := range []string{
		"", `null`, `[]`, `42`, `{"x":1} true`, `{"x":}`, `{"x":[1,]}`, `{"x":{"a":1,"\u0061":2}}`,
		`{"x":[{"a":1,"a":2}]}`, `{"x":"` + string([]byte{255}) + `"}`,
		`{"x":` + strings.Repeat("[", 66) + `0` + strings.Repeat("]", 66) + `}`,
	} {
		if _, err := alipayObject([]byte(raw)); err == nil {
			t.Errorf("invalid JSON accepted: %.80s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"x":[true,null,42,"x",{},[]]}`, `{"x":"\u0061\/b","unknown":true}`} {
		if _, err := alipayObject([]byte(raw)); err != nil {
			t.Fatalf("valid JSON rejected: %s", raw)
		}
	}
	object, err := alipayObject([]byte(`{ "node" : { "open_id" : "open\/id" } }`))
	if err != nil {
		t.Fatal(err)
	}
	if string(object["node"]) != `{ "open_id" : "open\/id" }` {
		t.Fatal("raw response node was normalized")
	}
	for _, value := range []json.RawMessage{json.RawMessage(`null`), json.RawMessage(`123`), json.RawMessage(`{}`), json.RawMessage(`[]`)} {
		if _, err := alipayString(map[string]json.RawMessage{"x": value}, "x"); err == nil {
			t.Fatal("non-string accepted")
		}
	}
}
