package accountsvc_test

import (
	"strings"
	"testing"
)

func TestSecretIndex(t *testing.T) {
	const code = "975629"
	for _, tc := range []struct {
		name, output, secret string
		found                bool
	}{
		{"code inside log timestamp fraction", `{"time":"2026-10-27T16:32:34.975629123Z","level":"INFO"}`, code, false},
		{"code at end of a longer digit run", `"elapsed":12975629`, code, false},
		{"code as the delivered value", "验证码：975629\n", code, true},
		{"code in key-value text", `code=975629 sent`, code, true},
		{"code as JSON number", `{"code":975629}`, code, true},
		{"code at output boundaries", "975629", code, true},
		{"later standalone occurrence after an embedded one", `34.975629123Z code=975629`, code, true},
		{"long secret still matches as substring", "prefix-private-blocked-token-suffix", "private-blocked-token", true},
		{"long numeric target still matches as substring", "+8613800000000", "13800000000", true},
		{"empty secret never matches", "anything", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := secretIndex(tc.output, tc.secret) >= 0; got != tc.found {
				t.Fatalf("secretIndex found=%v, want %v", got, tc.found)
			}
		})
	}
}

func TestRedactedContextMasksSecrets(t *testing.T) {
	secrets := []string{"fixture-smtp-password", "975629"}
	output := `{"msg":"x","detail":"fixture-smtp-password code=975629"}`
	got := redactedContext(output, strings.Index(output, "975629"), 6, secrets)
	if strings.Contains(got, "fixture-smtp-password") || strings.Contains(got, "975629") || !strings.Contains(got, "<secret#0>") || !strings.Contains(got, "code=<secret#1>") {
		t.Fatalf("context not redacted: %s", got)
	}
}
