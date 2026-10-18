package main

import (
	"bytes"
	"context"
	"testing"
)

func TestMetricsExamples(t *testing.T) {
	var output bytes.Buffer
	if err := run(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	want := "synthetic observations; no authentication performed\n" +
		"accountkit.auth.operation.duration instance=example operation=authenticate result=success count=2 sum_seconds=0.500\n" +
		"accountkit.auth.operations instance=example operation=authenticate result=success count=2\n"
	if output.String() != want {
		t.Fatalf("collected output:\n%s\nwant:\n%s", output.String(), want)
	}
}
