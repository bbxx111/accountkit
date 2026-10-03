package main

import (
	"strings"
	"testing"
)

func TestVerifyRequiresEveryTestToPass(t *testing.T) {
	cases := []struct {
		name, input string
		ok          bool
	}{
		{"passed", `{"Action":"pass","Package":"pkg","Test":"TestDB"}`, true},
		{"skipped", `{"Action":"skip","Package":"pkg","Test":"TestDB"}`, false},
		{"missing", `{"Action":"pass","Package":"pkg"}`, false},
		{"failed", `{"Action":"fail","Package":"pkg","Test":"TestDB"}`, false},
		{"package failed", `{"Action":"pass","Package":"pkg","Test":"TestDB"}
{"Action":"fail","Package":"pkg"}`, false},
		{"malformed", "not json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := verify(strings.NewReader(tc.input), []string{"pkg/TestDB"})
			if (err == nil) != tc.ok {
				t.Fatalf("verify=%v want success=%v", err, tc.ok)
			}
		})
	}
}
