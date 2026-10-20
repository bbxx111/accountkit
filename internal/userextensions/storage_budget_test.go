package userextensions

import (
	"bytes"
	"testing"
)

func TestStorageRepresentationBudget(t *testing.T) {
	expanded := append([]byte("{\n"), bytes.Repeat([]byte(" "), 17000)...)
	expanded = append(expanded, []byte("\"theme\":\"a\"}")...)
	if _, err := DecodePatch(expanded); err == nil {
		t.Fatal("raw patch exceeds 16KiB")
	}
	normalized, err := NormalizeStored(expanded)
	if err != nil || string(normalized) != `{"theme":"a"}` {
		t.Fatalf("storage whitespace: %s %v", normalized, err)
	}
	large := append([]byte(`{"theme":"`), bytes.Repeat([]byte("a"), 16384)...)
	large = append(large, []byte(`"}`)...)
	if _, err := NormalizeStored(large); err == nil {
		t.Fatal("canonical storage budget bypassed")
	}
	if _, err := NormalizeStored(bytes.Repeat([]byte(" "), 65537)); err == nil {
		t.Fatal("representation decoding must remain bounded")
	}
}
