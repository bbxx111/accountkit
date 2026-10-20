package userextensions

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/internal/extensionjson"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type denyCandidateLoader struct{}

func (denyCandidateLoader) Load(string) (any, error) {
	return nil, errors.New("external resources disabled")
}

// This executable candidate contract protects the precision and isolation
// assumptions on which the production schema adapter will rely.
func TestValidatorCandidate(t *testing.T) {
	decode := func(raw string) any {
		t.Helper()
		v, err := extensionjson.Decode([]byte(raw), extensionjson.Limits{MaxBytes: 65536, MaxDepth: 32})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	compiler := func() *jsonschema.Compiler {
		c := jsonschema.NewCompiler()
		c.DefaultDraft(jsonschema.Draft2020)
		c.AssertFormat()
		c.UseLoader(denyCandidateLoader{})
		c.UseRegexpEngine(func(s string) (jsonschema.Regexp, error) { return regexp.Compile(s) })
		return c
	}
	compile := func(raw string) *jsonschema.Schema {
		t.Helper()
		c := compiler()
		if err := c.AddResource("urn:accountkit:candidate", decode(raw)); err != nil {
			t.Fatal(err)
		}
		s, err := c.Compile("urn:accountkit:candidate")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	t.Run("integerUnnormalizedDecimal", func(t *testing.T) {
		if err := compile(`{"type":"integer"}`).Validate(json.Number("1.0")); err != nil {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name, schema, input string
		valid               bool
	}{
		{"integerDecimal", `{"type":"integer"}`, `1.0`, true},
		{"integerFraction", `{"type":"integer"}`, `1.5`, false},
		{"noCoercion", `{"type":"integer"}`, `"1"`, false},
		{"largeConstExact", `{"const":9007199254740993}`, `9007199254740993`, true},
		{"largeConstAdjacent", `{"const":9007199254740993}`, `9007199254740992`, false},
		{"multipleDecimal", `{"multipleOf":0.01}`, `0.29`, true},
		{"multipleDecimalInvalid", `{"multipleOf":0.01}`, `0.291`, false},
		{"multipleTiny", `{"multipleOf":0.00000000000000000001}`, `0.00000000000000000003`, true},
		{"multipleTinyInvalid", `{"multipleOf":0.00000000000000000001}`, `0.000000000000000000031`, false},
		{"dateValid", `{"format":"date"}`, `"2024-02-29"`, true},
		{"dateInvalid", `{"format":"date"}`, `"2023-02-29"`, false},
		{"dateTimeInvalid", `{"format":"date-time"}`, `"2023-02-29T00:00:00Z"`, false},
		{"emailInvalid", `{"format":"email"}`, `"not-an-email"`, false},
		{"uriInvalid", `{"format":"uri"}`, `"not a uri"`, false},
		{"uuidInvalid", `{"format":"uuid"}`, `"not-a-uuid"`, false},
		{"patternValid", `{"pattern":"^[a-z][a-z0-9]*(_[a-z0-9]+)*$"}`, `"user_name"`, true},
		{"patternInvalid", `{"pattern":"^[a-z][a-z0-9]*(_[a-z0-9]+)*$"}`, `"User"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := compile(`{"$schema":"https://json-schema.org/draft/2020-12/schema",` + tc.schema[1:])
			if err := s.Validate(decode(tc.input)); (err == nil) != tc.valid {
				t.Fatalf("valid=%v want %v: %v", err == nil, tc.valid, err)
			}
		})
	}
	t.Run("defaultIsAnnotation", func(t *testing.T) {
		s := compile(`{"type":"object","properties":{"x":{"type":"integer","default":5}}}`)
		v := decode(`{}`)
		if err := s.Validate(v); err != nil {
			t.Fatal(err)
		}
		if len(v.(map[string]any)) != 0 {
			t.Fatal("default mutated instance")
		}
	})
	t.Run("externalReferencesNoIO", func(t *testing.T) {
		var hits atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = w.Write([]byte(`true`)) }))
		defer server.Close()
		file := filepath.Join(t.TempDir(), "schema.json")
		if err := os.WriteFile(file, []byte(`true`), 0600); err != nil {
			t.Fatal(err)
		}
		for _, ref := range []string{server.URL + "/schema", "file:///" + filepath.ToSlash(file)} {
			c := compiler()
			if err := c.AddResource("urn:accountkit:candidate", map[string]any{"$ref": ref}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Compile("urn:accountkit:candidate"); err == nil {
				t.Fatal("external resource loaded")
			}
		}
		if hits.Load() != 0 {
			t.Fatal("external HTTP I/O occurred")
		}
	})
	t.Run("standardRegexpSubset", func(t *testing.T) {
		for _, pattern := range []string{`(?<=a)b`, `(a)\1`} {
			c := compiler()
			if err := c.AddResource("urn:accountkit:candidate", map[string]any{"pattern": pattern}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Compile("urn:accountkit:candidate"); err == nil {
				t.Fatal("unsupported regex accepted")
			}
		}
	})
	t.Run("compiledSchemaConcurrent", func(t *testing.T) {
		s := compile(`{"type":"object","properties":{"n":{"type":"integer","multipleOf":0.01},"s":{"type":"string","pattern":"^[a-z]+$"}}}`)
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					if err := s.Validate(map[string]any{"n": decode(`9007199254740993`), "s": "valid"}); err != nil {
						t.Error(err)
					}
					if err := s.Validate(map[string]any{"s": "INVALID"}); err == nil {
						t.Error("invalid concurrent instance accepted")
					}
				}
			}()
		}
		wg.Wait()
	})
}
