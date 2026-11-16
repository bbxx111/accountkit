package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestFixtureBaselineMatchesToolVersions(t *testing.T) {
	data, err := os.ReadFile("../../tool-versions.json")
	if err != nil {
		t.Fatal(err)
	}
	var versions struct {
		ServiceBaseline string `json:"serviceBaseline"`
	}
	if err := json.Unmarshal(data, &versions); err != nil {
		t.Fatal(err)
	}
	if serviceBaseline != versions.ServiceBaseline {
		t.Fatalf("fixture baseline %q != tools baseline %q", serviceBaseline, versions.ServiceBaseline)
	}
}
