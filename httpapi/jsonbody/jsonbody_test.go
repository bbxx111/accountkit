package jsonbody_test

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/httpapi/jsonbody"
)

func TestDecodeAcceptsObjectRejectsEverythingElse(t *testing.T) {
	type body struct {
		Reason string `json:"reason"`
	}
	for name, c := range map[string]struct {
		in string
		ok bool
	}{
		"object":        {`{"reason":"x"}`, true},
		"empty object":  {`{}`, true},
		"null":          {`null`, false},
		"array":         {`[]`, false},
		"string":        {`"x"`, false},
		"unknown field": {`{"nope":1}`, false},
		"trailing":      {`{"reason":"x"} {}`, false},
		"empty":         {``, false},
		"too large":     {`{"reason":"` + strings.Repeat("a", 70<<10) + `"}`, false},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/", strings.NewReader(c.in))
		var dst body
		err := jsonbody.Decode(rec, req, &dst, jsonbody.DefaultMaxBytes)
		if (err == nil) != c.ok {
			t.Fatalf("%s: err=%v", name, err)
		}
		if err != nil && !errors.Is(err, jsonbody.ErrMalformed) {
			t.Fatalf("%s: must wrap ErrMalformed: %v", name, err)
		}
	}
	if e := jsonbody.Malformed(); e.Status != "INVALID_ARGUMENT" || e.Reason != "MALFORMED_BODY" {
		t.Fatalf("Malformed(): %+v", e)
	}
}
