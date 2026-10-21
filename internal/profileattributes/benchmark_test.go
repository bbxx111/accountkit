package profileattributes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/boundedjson"
)

type policyBenchmarkFixture struct {
	name, schema, document          string
	wantBytes, wantDepth, wantNodes int
}

func policyBenchmarkFixtures() []policyBenchmarkFixture {
	props, values := make([]string, 128), make([]string, 128)
	for i := range props {
		props[i] = fmt.Sprintf(`"p%d":{"type":"string"}`, i)
		values[i] = fmt.Sprintf(`"p%d":"synthetic"`, i)
	}
	refProps, refValues := make([]string, 64), make([]string, 64)
	for i := range refProps {
		refProps[i] = fmt.Sprintf(`"p%d":{"$ref":"#/$defs/item"}`, i)
		refValues[i] = fmt.Sprintf(`"p%d":{"value":"synthetic"}`, i)
	}
	refs := strings.TrimSuffix(objectSchema(strings.Join(refProps, ",")), "}") + `,"$defs":{"item":{"type":"object","properties":{"value":{"type":"string"}},"additionalProperties":false}}}`
	nodes := strings.TrimSuffix(objectSchema(`"value":{"type":"integer"}`), "}") + `,"allOf":[` + strings.TrimSuffix(strings.Repeat("true,", 1022), ",") + `]}`
	large := `{"payload":"` + strings.Repeat("x", 16384-len(`{"payload":""}`)) + `"}`
	leafSchema := `{"type":"integer"}`
	for i := 0; i < 6; i++ {
		leafSchema = `{"type":"array","items":` + leafSchema + `}`
	}
	deep := `{"values":` + strings.Repeat("[", 6) + strings.TrimSuffix(strings.Repeat("1,", 505), ",") + strings.Repeat("]", 6) + `}`
	return []policyBenchmarkFixture{
		{name: "representative", schema: objectSchema(`"count":{"type":"integer"},"label":{"type":"string"},"preferences":{"type":"object","properties":{"theme":{"type":"string","enum":["LIGHT","DARK"]},"notifications":{"type":"boolean"}},"additionalProperties":false},"tags":{"type":"array","items":{"type":"string"}}`), document: `{"count":9007199254740993,"label":"synthetic","preferences":{"theme":"DARK","notifications":true},"tags":["one","two"]}`},
		{name: "properties128", schema: objectSchema(strings.Join(props, ",")), document: `{` + strings.Join(values, ",") + `}`, wantNodes: 129, wantDepth: 2},
		{name: "expandedNodes1024", schema: nodes, document: `{"value":9007199254740993}`, wantNodes: 2, wantDepth: 2},
		{name: "reusedLocalRefs128Properties", schema: refs, document: `{` + strings.Join(refValues, ",") + `}`, wantNodes: 129, wantDepth: 3},
		{name: "bytes16KiB", schema: objectSchema(`"payload":{"type":"string"}`), document: large, wantBytes: 16384, wantNodes: 2, wantDepth: 2},
		{name: "depth8ValueNodes512", schema: objectSchema(`"values":` + leafSchema), document: deep, wantDepth: 8, wantNodes: 512},
	}
}

func preparePolicyBenchmark(tb testing.TB, f policyBenchmarkFixture) (*Policy, *Patch, []byte) {
	tb.Helper()
	p, err := Compile("benchmark-v1", []byte(f.schema))
	if err != nil {
		tb.Fatalf("invalid benchmark schema: %v", err)
	}
	patch, err := DecodePatch([]byte(f.document))
	if err != nil {
		tb.Fatalf("invalid benchmark document: %v", err)
	}
	got, changed, err := p.Apply([]byte(`{}`), patch)
	if err != nil || !changed {
		tb.Fatalf("benchmark must exercise a successful change: changed=%v err=%v", changed, err)
	}
	v, err := boundedjson.Decode(got, boundedjson.Limits{MaxBytes: 16384, MaxDepth: 8, MaxNodes: 512})
	if err != nil {
		tb.Fatal(err)
	}
	depth, nodes := benchmarkValueShape(v)
	if f.wantBytes != 0 && (len(f.document) != f.wantBytes || len(got) != f.wantBytes) || f.wantDepth != 0 && depth != f.wantDepth || f.wantNodes != 0 && nodes != f.wantNodes {
		tb.Fatalf("fixture shape bytes=%d/%d depth=%d nodes=%d", len(f.document), len(got), depth, nodes)
	}
	if strings.Contains(f.document, "9007199254740993") && !bytes.Contains(got, []byte("9007199254740993")) {
		tb.Fatal("benchmark fixture lost integer precision")
	}
	return p, patch, got
}

// Count the actual decoded value independently of the schema/compiler budgets.
func benchmarkValueShape(v any) (depth, nodes int) {
	depth, nodes = 1, 1
	visit := func(child any) {
		d, n := benchmarkValueShape(child)
		if d+1 > depth {
			depth = d + 1
		}
		nodes += n
	}
	switch x := v.(type) {
	case map[string]any:
		for _, child := range x {
			visit(child)
		}
	case []any:
		for _, child := range x {
			visit(child)
		}
	}
	return depth, nodes
}

// A too-strict budget or malformed fixture must fail here instead of turning
// the benchmark into timing only early rejection. Also pin the adjacent schema
// rejection boundaries for the two exact expanded limits.
func TestPolicyBenchmarkFixtures(t *testing.T) {
	for _, f := range policyBenchmarkFixtures() {
		t.Run(f.name, func(t *testing.T) {
			p, patch, want := preparePolicyBenchmark(t, f)
			got, changed, err := p.Apply(want, patch)
			if err != nil || changed || !bytes.Equal(got, want) {
				t.Fatal("same successful benchmark patch should be a stable no-op", err)
			}
			if f.name == "expandedNodes1024" {
				over := strings.Replace(f.schema, `"allOf":[`, `"allOf":[true,`, 1)
				if _, err := Compile("benchmark-v1", []byte(over)); err == nil {
					t.Fatal("1025 schema nodes accepted")
				}
			}
			if f.name == "properties128" {
				var schema map[string]json.RawMessage
				if err := json.Unmarshal([]byte(f.schema), &schema); err != nil {
					t.Fatal(err)
				}
				var props map[string]json.RawMessage
				if err := json.Unmarshal(schema["properties"], &props); err != nil {
					t.Fatal(err)
				}
				props["extra"] = json.RawMessage(`{"type":"string"}`)
				schema["properties"], _ = json.Marshal(props)
				over, err := json.Marshal(schema)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Compile("benchmark-v1", over); err == nil {
					t.Fatal("129 schema properties accepted")
				}
			}
		})
	}
}

func BenchmarkCompile(b *testing.B) {
	for _, f := range policyBenchmarkFixtures() {
		b.Run(f.name, func(b *testing.B) {
			preparePolicyBenchmark(b, f)
			raw := []byte(f.schema)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := Compile("benchmark-v1", raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkApply(b *testing.B) {
	for _, f := range policyBenchmarkFixtures() {
		b.Run(f.name, func(b *testing.B) {
			p, patch, _ := preparePolicyBenchmark(b, f)
			current := []byte(`{}`)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, changed, err := p.Apply(current, patch); err != nil || !changed {
					b.Fatalf("changed=%v err=%v", changed, err)
				}
			}
		})
	}
}
