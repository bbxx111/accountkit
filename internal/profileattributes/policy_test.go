package profileattributes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/bbxx111/accountkit/internal/boundedjson"
)

const dialect = `"$schema":"https://json-schema.org/draft/2020-12/schema",`

func objectSchema(properties string) string {
	return `{` + dialect + `"type":"object","properties":{` + properties + `},"additionalProperties":false}`
}
func compileTest(t *testing.T, schema string) *Policy {
	t.Helper()
	p, err := Compile("v1", json.RawMessage(schema))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompilePolicy(t *testing.T) {
	valid := objectSchema(`"bio":{"type":"string"}`)
	for _, v := range []string{"", "bad version", strings.Repeat("x", 65), "中文"} {
		if _, err := Compile(v, []byte(valid)); err == nil {
			t.Fatalf("version %q accepted", v)
		}
	}
	for _, raw := range []string{"", "null", `{}`, strings.Replace(valid, "2020-12", "2019-09", 1), strings.Replace(valid, `"properties"`, `"propertiez"`, 1), objectSchema(`"id":{"type":"string"}`), objectSchema(`"profile_attributes":{"type":"string"}`), objectSchema(`"Bad":{"type":"string"}`), objectSchema(`"x":{"type":["string","integer"]}`), objectSchema(`"x":{}`), objectSchema(`"x":{"type":"null"}`), objectSchema(`"x":{"$id":"urn:nested","type":"string"}`), objectSchema(`"x":{"type":"string","format":"hostname"}`), objectSchema(`"x":{"type":"string","pattern":"(?<=a)b"}`), objectSchema(`"x":{"type":"array","prefixItems":[{"type":"string"}]}`), objectSchema(`"x":{"type":"object","patternProperties":{},"additionalProperties":false}`), strings.TrimSuffix(valid, "}") + `,"required":["bio"]}`, objectSchema(`"x":{"$ref":"https://example.invalid/schema"}`), `{` + dialect + `"type":"object","properties":{"x":{"$ref":"#/$defs/a"}},"additionalProperties":false,"$defs":{"a":{"$ref":"#/$defs/a"}}}`} {
		if _, err := Compile("v1", []byte(raw)); err == nil {
			t.Errorf("invalid schema accepted: %s", raw)
		}
	}
	t.Run("refsAndConstraints", func(t *testing.T) {
		raw := `{` + dialect + `"type":"object","properties":{"a":{"$ref":"#/$defs/text"},"b":{"$ref":"#/$defs/text"}},"additionalProperties":false,"$defs":{"text":{"type":"string","maxLength":5}},"if":{"required":["a"]},"then":{"required":["b"],"properties":{"a":{"enum":["yes"]}}}}`
		p := compileTest(t, raw)
		patch, _ := DecodePatch([]byte(`{"a":"no","b":"ok"}`))
		if _, _, err := p.Apply([]byte(`{}`), patch); err == nil {
			t.Fatal("conditional ignored")
		}
		for _, bad := range []string{strings.Replace(raw, `"a":{"enum"`, `"unknown":{"enum"`, 1), strings.Replace(raw, `"a":{"enum":["yes"]}`, `"a":{"type":"integer"}`, 1)} {
			if _, err := Compile("v1", []byte(bad)); err == nil {
				t.Fatal("branch changed declaration")
			}
		}
	})
	t.Run("fingerprint", func(t *testing.T) {
		p := compileTest(t, valid)
		q := compileTest(t, " \n"+valid)
		if p.Version() != "v1" || len(p.Fingerprint()) != 64 || p.Fingerprint() != q.Fingerprint() {
			t.Fatal("unstable identity")
		}
		q = compileTest(t, strings.TrimSuffix(valid, "}")+`,"description":"annotation"}`)
		if p.Fingerprint() == q.Fingerprint() {
			t.Fatal("annotation ignored")
		}
	})
	t.Run("expandedProperties", func(t *testing.T) {
		props := []string{}
		for i := 0; i < 129; i++ {
			props = append(props, fmt.Sprintf(`"p%d":{"type":"string"}`, i))
		}
		if _, err := Compile("v1", []byte(objectSchema(strings.Join(props, ",")))); err == nil {
			t.Fatal("property cap ignored")
		}
	})
}

func TestPolicyMergePatch(t *testing.T) {
	p := compileTest(t, objectSchema(`"bio":{"type":"string"},"preferences":{"type":"object","properties":{"theme":{"type":"string","enum":["LIGHT","DARK"]},"notifications":{"type":"boolean"}},"additionalProperties":false,"required":["theme"]},"count":{"type":"integer"},"list":{"type":"array","items":{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}}`))
	for _, tc := range []struct {
		name, current, patch, want string
		changed, invalid           bool
	}{
		{"recursive", `{"bio":"old","preferences":{"theme":"LIGHT","notifications":true}}`, `{"bio":null,"preferences":{"theme":"DARK"}}`, `{"preferences":{"notifications":true,"theme":"DARK"}}`, true, false},
		{"unknownDelete", `{}`, `{"secret_untrusted":null}`, "", false, true},
		{"arrayUnknown", `{}`, `{"list":[{"secret_untrusted":null}]}`, "", false, true},
		{"arrayNull", `{}`, `{"list":[null]}`, "", false, true},
		{"arrayPropertyNull", `{}`, `{"list":[{"value":null}]}`, "", false, true},
		{"existingEmpty", `{"preferences":{"theme":"LIGHT"}}`, `{"preferences":{}}`, `{"preferences":{"theme":"LIGHT"}}`, false, false},
		{"missingEmpty", `{}`, `{"preferences":{}}`, "", false, true},
		{"deleteParent", `{"preferences":{"theme":"LIGHT"}}`, `{"preferences":null}`, `{}`, true, false},
		{"deleteAbsent", `{}`, `{"bio":null}`, `{}`, false, false},
		{"deleteRequired", `{"preferences":{"theme":"LIGHT"}}`, `{"preferences":{"theme":null}}`, "", false, true},
		{"unchangedInvalid", `{"preferences":{}}`, `{}`, "", false, true},
		{"numericEqual", `{"count":1.0}`, `{"count":1e0}`, `{"count":1}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.patch)
			patch, err := DecodePatch(raw)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), raw...)
			next, changed, err := p.Apply([]byte(tc.current), patch)
			if tc.invalid {
				if err == nil {
					t.Fatal("accepted invalid result")
				}
				var ve *ValidationError
				if !errors.As(err, &ve) {
					t.Fatalf("not safe validation error: %T", err)
				}
				if strings.Contains(err.Error(), "secret_untrusted") || errors.Unwrap(err) != nil {
					t.Fatal("unsafe validation details")
				}
				return
			}
			if err != nil || string(next) != tc.want || changed != tc.changed {
				t.Fatalf("got %s %v %v want %s %v", next, changed, err, tc.want, tc.changed)
			}
			if string(raw) != string(before) {
				t.Fatal("input mutated")
			}
			if len(next) > 0 {
				next[0] = 'x'
			}
			again, _, err := p.Apply([]byte(tc.current), patch)
			if err != nil || string(again) != tc.want {
				t.Fatal("mutable result/patch alias")
			}
		})
	}
	t.Run("concurrentReuse", func(t *testing.T) {
		patch, _ := DecodePatch([]byte(`{"bio":"new"}`))
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					if _, _, err := p.Apply([]byte(`{}`), patch); err != nil {
						t.Error(err)
					}
				}
			}()
		}
		wg.Wait()
	})
}

func TestPatchAndStoredBoundaries(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"x":1,"x":2}`, `{} {}`, `{"x":"\u0000"}`, `{"x":` + strings.Repeat("[", 8) + `0` + strings.Repeat("]", 8) + `}`, `{"x":"` + strings.Repeat("a", 16384) + `"}`} {
		if _, err := DecodePatch([]byte(raw)); err == nil {
			t.Fatalf("invalid patch accepted: %.40s", raw)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"x":null}`, `{"x":[null]}`} {
		if _, err := NormalizeStored([]byte(raw)); err == nil {
			t.Fatal("invalid stored accepted")
		}
	}
	got, err := NormalizeStored([]byte(`{"x":1.0}`))
	if err != nil || string(got) != `{"x":1}` {
		t.Fatal(string(got), err)
	}
	patch, err := DecodePatch([]byte(`{"undeclared":null}`))
	if err != nil || patch == nil {
		t.Fatal("path checked before Apply")
	}
}

func TestPolicyConstraintDefinitionsAndBooleanFragments(t *testing.T) {
	raw := `{` + dialect + `"type":"object","properties":{"flag":{"type":"boolean"},"word":{"type":"string"}},"additionalProperties":false,"$defs":{"word_constraint":{"enum":["yes"]},"deny":false,"unused":{"description":"annotation only"}},"allOf":[true],"if":{"properties":{"flag":{"const":true}},"required":["flag"]},"then":{"properties":{"word":{"$ref":"#/$defs/word_constraint"}},"required":["word"]},"else":{"not":{"$ref":"#/$defs/deny"}}}`
	p := compileTest(t, raw)
	for _, tc := range []struct {
		patch string
		valid bool
	}{{`{"flag":true,"word":"yes"}`, true}, {`{"flag":true,"word":"no"}`, false}, {`{"flag":false}`, true}} {
		patch, err := DecodePatch([]byte(tc.patch))
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = p.Apply([]byte(`{}`), patch)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", tc.patch, err)
		}
	}
	for _, raw := range []string{objectSchema(`"x":true`), objectSchema(`"x":false`), `{` + dialect + `"type":"object","properties":{},"additionalProperties":false,"$defs":{"unused":{"typo":true}}}`, `{` + dialect + `"type":"object","properties":{},"additionalProperties":false,"$defs":{"unused":{"$ref":"#/$defs/unused"}}}`} {
		if _, err := Compile("v1", []byte(raw)); err == nil {
			t.Fatal("unsafe definition/structure accepted")
		}
	}
}

func TestPolicyExpandedBudgets(t *testing.T) {
	props := make([]string, 128)
	for i := range props {
		props[i] = fmt.Sprintf(`"p%d":{"type":"string"}`, i)
	}
	raw := strings.TrimSuffix(objectSchema(strings.Join(props, ",")), "}") + `,"if":{"required":["p0"]},"then":{"properties":{"p0":{"minLength":1}}}}`
	compileTest(t, raw)
	for _, count := range []int{1023, 1024} {
		conditions := strings.TrimSuffix(strings.Repeat("true,", count), ",")
		raw := strings.TrimSuffix(objectSchema(""), "}") + `,"allOf":[` + conditions + `]}`
		_, err := Compile("v1", []byte(raw))
		if (err == nil) != (count == 1023) {
			t.Fatalf("root + %d nodes: %v", count, err)
		}
	}
	// 64 repeated object definitions add 64 outer and 64 inner declarations;
	// one extra outer reference crosses the property boundary.
	for _, count := range []int{64, 65} {
		props := make([]string, count)
		for i := range props {
			props[i] = fmt.Sprintf(`"p%d":{"$ref":"#/$defs/item"}`, i)
		}
		raw := strings.TrimSuffix(objectSchema(strings.Join(props, ",")), "}") + `,"$defs":{"item":{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}}}`
		_, err := Compile("v1", []byte(raw))
		if (err == nil) != (count == 64) {
			t.Fatalf("expanded properties %d: %v", count*2, err)
		}
	}
	// Reused constraints expand only to the node budget, never as declarations.
	raw = strings.TrimSuffix(objectSchema(`"x":{"type":"string"}`), "}") + `,"$defs":{"a":{"allOf":[{"$ref":"#/$defs/b"},{"$ref":"#/$defs/b"}]},"b":{"allOf":[{"$ref":"#/$defs/c"},{"$ref":"#/$defs/c"}]},"c":{"allOf":[` + strings.TrimSuffix(strings.Repeat("true,", 300), ",") + `]}},"allOf":[{"$ref":"#/$defs/a"}]}`
	if _, err := Compile("v1", []byte(raw)); err == nil {
		t.Fatal("reference expansion bypassed node budget")
	}
}

func TestPolicyOfflineAndReferences(t *testing.T) {
	raw := strings.TrimSuffix(objectSchema(`"x":{"$ref":"#/$defs/a~1b"}`), "}") + `,"$id":"https://example.invalid/schema","$defs":{"a/b":{"type":"string"}}}`
	compileTest(t, raw)
	compileTest(t, strings.Replace(raw, "#/$defs/a~1b", "#/$defs/a%7E1b", 1))
	for _, raw := range []string{
		strings.TrimSuffix(objectSchema(""), "}") + `,"$defs":{"unused":{"$ref":"file:///does-not-exist.json"}}}`,
		strings.TrimSuffix(objectSchema(`"x":{"$ref":"#/$defs/a/default"}`), "}") + `,"$defs":{"a":{"default":{"type":"string","typo":true}}}}`,
		strings.TrimSuffix(objectSchema(`"x":{"type":"string"}`), "}") + `,"allOf":[{"required":["unknown"]}]}`,
		strings.TrimSuffix(objectSchema(`"x":{"type":"string"}`), "}") + `,"dependentRequired":{"x":["unknown"]}}`,
	} {
		if _, err := Compile("v1", []byte(raw)); err == nil {
			t.Fatal("invalid reference or undeclared constraint accepted")
		}
	}
}

func TestPolicyViolationsAreBoundedAndSafe(t *testing.T) {
	props := make([]string, 24)
	values := make([]string, 24)
	for i := range props {
		props[i] = fmt.Sprintf(`"p%d":{"type":"string","const":"expected"}`, i)
		values[i] = fmt.Sprintf(`"p%d":"private_value_marker"`, i)
	}
	p := compileTest(t, objectSchema(strings.Join(props, ",")))
	patch, _ := DecodePatch([]byte(`{` + strings.Join(values, ",") + `}`))
	_, _, err := p.Apply([]byte(`{}`), patch)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Violations) != 16 || strings.Contains(fmt.Sprintf("%+v", ve), "private_value_marker") {
		t.Fatalf("unsafe/unbounded result: %#v", ve)
	}
	p = compileTest(t, objectSchema(`"nested":{"type":"object","properties":{"known":{"type":"string"}},"additionalProperties":false}`))
	patch, _ = DecodePatch([]byte(`{"nested":{"private_key_marker":null}}`))
	_, _, err = p.Apply([]byte(`{}`), patch)
	if !errors.As(err, &ve) || ve.Violations[0].Path != "/nested" || ve.Violations[0].Rule != "additionalProperties" {
		t.Fatalf("nearest parent: %#v", ve)
	}
}

func TestPolicyMergedResultLimitsAndOwnership(t *testing.T) {
	p := compileTest(t, objectSchema(`"a":{"type":"string"},"b":{"type":"string"},"first":{"type":"array","items":{"type":"integer"}},"second":{"type":"array","items":{"type":"integer"}}`))
	patch, _ := DecodePatch([]byte(`{"b":"` + strings.Repeat("b", 9000) + `"}`))
	if _, _, err := p.Apply([]byte(`{"a":"`+strings.Repeat("a", 9000)+`"}`), patch); err == nil {
		t.Fatal("merged bytes bypassed cap")
	}
	array := `[` + strings.TrimSuffix(strings.Repeat("1,", 260), ",") + `]`
	patch, _ = DecodePatch([]byte(`{"second":` + array + `}`))
	if _, _, err := p.Apply([]byte(`{"first":`+array+`}`), patch); err == nil {
		t.Fatal("merged nodes bypassed cap")
	}
	raw := []byte(`{"a":"original"}`)
	patch, _ = DecodePatch(raw)
	for i := range raw {
		raw[i] = 'x'
	}
	got, _, err := p.Apply([]byte(`{}`), patch)
	if err != nil || string(got) != `{"a":"original"}` {
		t.Fatal("patch retained caller bytes")
	}
	schema := []byte(objectSchema(`"n":{"type":"number","minimum":1.0}`))
	q, err := Compile("v1", schema)
	if err != nil {
		t.Fatal(err)
	}
	r := compileTest(t, strings.Replace(string(schema), "1.0", "1e0", 1))
	if q.Fingerprint() != r.Fingerprint() {
		t.Fatal("fingerprint used lexical numbers")
	}
	for i := range schema {
		schema[i] = 'x'
	}
	patch, _ = DecodePatch([]byte(`{"n":2}`))
	if _, _, err := q.Apply([]byte(`{}`), patch); err != nil {
		t.Fatal("policy retained caller schema bytes")
	}
}

func TestPolicyUnusedAdditionalPropertiesCannotHideSchemas(t *testing.T) {
	for _, value := range []string{`{"typo":true}`, `{"format":"hostname"}`, `{"$ref":"https://example.invalid/schema"}`, `{"$id":"urn:nested"}`, `true`, `null`, `[]`} {
		raw := strings.TrimSuffix(objectSchema(""), "}") + `,"$defs":{"unused":{"additionalProperties":` + value + `}}}`
		if _, err := Compile("v1", []byte(raw)); err == nil {
			t.Errorf("unsupported additionalProperties value accepted: %s", value)
		}
	}
	compileTest(t, strings.TrimSuffix(objectSchema(""), "}")+`,"$defs":{"unused":{"additionalProperties":false}}}`)
}

func TestPolicyUnusedDefinitionBudgetIsIndependentOfScanOrder(t *testing.T) {
	raw := strings.TrimSuffix(objectSchema(""), "}") + `,"$defs":{"a":{"allOf":[{"$ref":"#/$defs/b"}]},"b":{"allOf":[` + strings.TrimSuffix(strings.Repeat("true,", 600), ",") + `]}}}`
	// Exercise both orders explicitly; relying only on randomized map iteration
	// would make a regression test itself nondeterministic.
	for _, first := range []string{"a", "b"} {
		t.Run(first+"First", func(t *testing.T) {
			value, err := boundedjson.Decode([]byte(raw), boundedjson.Limits{MaxBytes: 65536, MaxDepth: 32})
			if err != nil {
				t.Fatal(err)
			}
			checker := schemaChecker{}
			if err := checker.syntax(value, true); err != nil {
				t.Fatal(err)
			}
			if _, err := checker.structure(value, map[string]bool{}, true); err != nil {
				t.Fatal(err)
			}
			if checker.definitionsList[0].path != "/$defs/"+first {
				checker.definitionsList[0], checker.definitionsList[1] = checker.definitionsList[1], checker.definitionsList[0]
			}
			if err := checker.definitions(value.(map[string]any)); err != nil {
				t.Fatal("deduplicated unused graph below node cap rejected", err)
			}
		})
	}
	for i := 0; i < 100; i++ {
		if _, err := Compile("v1", []byte(raw)); err != nil {
			t.Fatalf("same schema rejected at attempt %d: %v", i, err)
		}
	}
	shared := strings.Replace(raw, `"$defs":{`, `"$defs":{"c":{"allOf":[{"$ref":"#/$defs/b"}]},`, 1)
	compileTest(t, shared)
	// Deduplication is restricted to unused safety checks. Actual consuming
	// references still expand at every position and exceed the node budget.
	consumed := strings.TrimSuffix(shared, "}") + `,"allOf":[{"$ref":"#/$defs/a"},{"$ref":"#/$defs/c"}]}`
	if _, err := Compile("v1", []byte(consumed)); err == nil {
		t.Fatal("actual consuming positions were incorrectly deduplicated")
	}
	overLimit := strings.TrimSuffix(objectSchema(""), "}") + `,"$defs":{"unused":{"allOf":[` + strings.TrimSuffix(strings.Repeat("true,", 1023), ",") + `]}}}`
	if _, err := Compile("v1", []byte(overLimit)); err == nil {
		t.Fatal("unique unused nodes bypassed total node budget")
	}
}
