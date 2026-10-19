package main

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
)

func TestMetricsExamples(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	actual := output.String()
	lines := strings.Split(actual, "\n")
	for _, want := range []string{
		"# synthetic observations; no authentication performed",
		`accountkit_auth_operations_total{accountkit_instance="example",grant="user",method="none",mode="none",operation="authenticate",reason="none",result="success"} 2`,
		`accountkit_auth_operation_duration_seconds_bucket{accountkit_instance="example",method="none",operation="authenticate",result="success",le="0.25"} 1`,
		`accountkit_auth_operation_duration_seconds_sum{accountkit_instance="example",method="none",operation="authenticate",result="success"} 0.5`,
		`accountkit_auth_operation_duration_seconds_count{accountkit_instance="example",method="none",operation="authenticate",result="success"} 2`,
	} {
		if !slices.Contains(lines, want) {
			t.Fatalf("missing complete line %q in collected output:\n%s", want, actual)
		}
	}
}
