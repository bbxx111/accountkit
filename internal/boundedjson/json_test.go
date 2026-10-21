package boundedjson

import (
	"encoding/json"
	"strings"
	"testing"
)

var profileLimits = Limits{MaxBytes: 16384, MaxDepth: 8, MaxNodes: 512}

func TestDecodeRejectsAmbiguousJSON(t *testing.T) {
	invalid := []string{`{"x":1,"x":2}`, `{"x":1,"\u0078":2}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, `{"x":"\ud800\u0041"}`, `{"x":"\u0000"}`, `{"x":1}{"y":2}`, `{"x":1e999999999}`, `{"x":1e-999999999}`, "{\"x\":\"\xff\"}", `{"secret-key": [}`, `{"x":01}`}
	for _, raw := range invalid {
		t.Run(raw, func(t *testing.T) {
			if _, err := Decode([]byte(raw), profileLimits); err == nil {
				t.Fatal("accepted invalid JSON")
			} else if strings.Contains(err.Error(), "secret-key") {
				t.Fatal("error leaked input")
			}
		})
	}
	v, err := Decode([]byte(`{"x":"\ud83d\ude00","n":null}`), profileLimits)
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["x"] != "😀" || v.(map[string]any)["n"] != nil {
		t.Fatal("lost unicode/null")
	}
}

func TestCanonicalNumbers(t *testing.T) {
	cases := map[string]string{"1": "1", "1.0": "1", "1e0": "1", "-0.000": "0", "12.3400e-2": "0.1234", "1e3": "1000", "9007199254740993": "9007199254740993", "0.00001": "0.00001"}
	for input, want := range cases {
		v, err := Decode([]byte(`{"n":`+input+`}`), profileLimits)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Canonical(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != `{"n":`+want+`}` {
			t.Errorf("%s: got %s", input, b)
		}
	}
	for _, input := range []string{"1e16384", "1e-16384", "1e131072", "1e999999999"} {
		if _, err := Decode([]byte(input), profileLimits); err == nil {
			t.Errorf("accepted out-of-budget/range number %s", input)
		}
	}
	if _, err := Decode([]byte("1e1900"), profileLimits); err != nil {
		t.Fatal("valid expansion rejected", err)
	}
	for _, v := range []any{float64(1), json.Number("NaN"), json.Number("01"), json.Number("1 "), json.Number(" 1"), map[string]any{"bad\x00": true}} {
		if _, err := Canonical(v); err == nil {
			t.Errorf("accepted invalid canonical type/value %T", v)
		}
	}
}

func TestDecodeLimits(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		limits    Limits
		ok        bool
	}{
		{"depth8", strings.Repeat("[", 7) + "0" + strings.Repeat("]", 7), profileLimits, true},
		{"depth9", strings.Repeat("[", 8) + "0" + strings.Repeat("]", 8), profileLimits, false},
		{"nodes512", "[" + strings.Repeat("0,", 510) + "0]", profileLimits, true},
		{"nodes513", "[" + strings.Repeat("0,", 511) + "0]", profileLimits, false},
		{"whitespace", strings.Repeat(" ", 16384) + "{}", profileLimits, false},
		{"schema32", strings.Repeat("[", 31) + "null" + strings.Repeat("]", 31), Limits{65536, 32, 0}, true},
		{"schema33", strings.Repeat("[", 32) + "null" + strings.Repeat("]", 32), Limits{65536, 32, 0}, false},
		{"schemaBytes", `"` + strings.Repeat("x", 65535) + `"`, Limits{65536, 32, 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.raw), tc.limits)
			if (err == nil) != tc.ok {
				t.Fatalf("accepted=%v want %v: %v", err == nil, tc.ok, err)
			}
		})
	}
}

func TestCloneAndCanonicalObjects(t *testing.T) {
	original, err := Decode([]byte(`{"z":[{"x":1}],"a":"<>&"}`), profileLimits)
	if err != nil {
		t.Fatal(err)
	}
	copied := Clone(original).(map[string]any)
	copied["z"].([]any)[0].(map[string]any)["x"] = json.Number("2")
	b, err := Canonical(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"a":"<>&","z":[{"x":1}]}` {
		t.Fatalf("mutated original/noncanonical object: %s", b)
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	if _, err := Canonical(cyclic); err == nil {
		t.Fatal("accepted cyclic value")
	}
}

func TestNumericStorageBoundsAndCanonicalBudget(t *testing.T) {
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"1e131071", true}, {"1e131072", false}, {"1e-16383", true}, {"1e-16384", false},
	} {
		v, err := Decode([]byte(tc.raw), Limits{MaxBytes: 150000, MaxDepth: 32})
		if (err == nil) != tc.ok {
			t.Fatalf("number %s accepted=%v want=%v", tc.raw, err == nil, tc.ok)
		}
		if tc.raw == "1e131071" {
			if _, err := Canonical(v); err == nil {
				t.Fatal("canonical exceeded generic 64 KiB budget")
			}
		}
	}
	if _, err := Canonical(strings.Repeat("x", 65535)); err == nil {
		t.Fatal("canonical string exceeded budget")
	}
	if _, err := Canonical(strings.Repeat("x", 65534)); err != nil {
		t.Fatal(err)
	}
}

// A final size error alone does not prevent amplification: each small exponent
// must share the document budget before its expanded string is allocated.
func TestDecodeAggregateExpansionBudget(t *testing.T) {
	raw := []byte("[" + strings.Repeat("1e16000,", 254) + "1e16000]")
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := Decode(raw, Limits{MaxBytes: 16384, MaxDepth: 8}); err == nil {
				b.Fatal("accepted oversized aggregate")
			}
		}
	})
	// The prior parser retains over 4 MiB of expanded numbers for this 2 KiB
	// input. A 512 KiB ceiling leaves ample room for decoder/runtime overhead
	// while requiring rejection before most expansions occur.
	if allocated := result.AllocedBytesPerOp(); allocated > 512*1024 {
		t.Fatalf("oversized aggregate allocated %d bytes/op; need early rejection below 512 KiB", allocated)
	}
	t.Logf("bounded aggregate allocation: %d bytes/op", result.AllocedBytesPerOp())
}

func TestDecodeCanonicalBudgetAccountsForWholeDocument(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`[true,false,null,1e10]`, `[true,false,null,10000000000]`},
		{"{\"\u2028\":[\"\\n\",1e10]}", `{"\u2028":["\n",10000000000]}`},
		{"[\"\\\"\\\\\\b\\f\\n\\r\\t\\u0001\u2029😀\",1e10]", `["\"\\\b\f\n\r\t\u0001\u2029😀",10000000000]`},
	} {
		limits := Limits{MaxBytes: len(tc.want), MaxDepth: 8}
		v, err := Decode([]byte(tc.raw), limits)
		if err != nil {
			t.Fatal(err)
		}
		canonical, err := Canonical(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(canonical) != tc.want {
			t.Fatalf("canonical mismatch: %q", canonical)
		}
		limits.MaxBytes--
		if _, err := Decode([]byte(tc.raw), limits); err == nil {
			t.Fatal("accepted document one byte over canonical budget")
		}
	}
}
