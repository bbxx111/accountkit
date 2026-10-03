package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestMainRejectsUnknownCommandWithoutArgumentLeaks(t *testing.T) {
	var output bytes.Buffer
	code := runMain(context.Background(), []string{"synthetic-command-secret"}, slog.New(slog.NewTextHandler(&output, nil)))
	if code != 1 || strings.Contains(output.String(), "synthetic-command-secret") || !strings.Contains(output.String(), "serve or migrate") {
		t.Fatal("CLI did not safely report command failure")
	}
}

func TestMainConfigErrorDoesNotLeakInvalidValue(t *testing.T) {
	t.Setenv("ACCOUNTKIT_ACCESS_TOKEN_TTL", "synthetic-private-secret")
	var output bytes.Buffer
	code := runMain(context.Background(), nil, slog.New(slog.NewTextHandler(&output, nil)))
	if code != 1 || strings.Contains(output.String(), "synthetic-private-secret") || !strings.Contains(output.String(), "ACCOUNTKIT_ACCESS_TOKEN_TTL") {
		t.Fatal("CLI configuration error leaked input or omitted setting")
	}
}
