package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestMainRejectsUnknownCommandWithoutArgumentLeaks(t *testing.T) {
	var output bytes.Buffer
	code := runMain(context.Background(), []string{"synthetic-command-secret"}, slog.New(slog.NewTextHandler(&output, nil)))
	if code != 1 || strings.Contains(output.String(), "synthetic-command-secret") || !strings.Contains(output.String(), "serve, migrate or email-templates check") {
		t.Fatal("CLI did not safely report command failure")
	}
}

func TestMainConfigErrorDoesNotLeakInvalidValue(t *testing.T) {
	// CI exports ACCOUNTKIT_RECOVERY_* for the recovery drill; accountsvc rejects any
	// non-empty ACCOUNTKIT_ variable before reading settings, so blank them here.
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); strings.HasPrefix(name, "ACCOUNTKIT_") {
			t.Setenv(name, "")
		}
	}
	t.Setenv("ACCOUNTSVC_ACCESS_TOKEN_TTL", "synthetic-private-secret")
	var output bytes.Buffer
	code := runMain(context.Background(), nil, slog.New(slog.NewTextHandler(&output, nil)))
	if code != 1 || strings.Contains(output.String(), "synthetic-private-secret") || !strings.Contains(output.String(), "ACCOUNTSVC_ACCESS_TOKEN_TTL") {
		t.Fatal("CLI configuration error leaked input or omitted setting")
	}
}
