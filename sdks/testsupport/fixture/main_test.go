package main

import (
	"context"
	"testing"
)

func TestMissingDependenciesFails(t *testing.T) {
	if _, err := start(context.Background(), options{}); err == nil {
		t.Fatal("missing database and Redis must fail")
	}
}
func TestCaptureSeparatesPurpose(t *testing.T) {
	s := newCapture()
	if _, ok := s.get("a", "SIGN_IN"); ok {
		t.Fatal("empty capture returned code")
	}
}
