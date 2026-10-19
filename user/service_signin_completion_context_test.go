package user_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// A persisted ticket must survive a host switching from the deprecated context
// field to the new one. The legacy JSON keys/order and AAD are storage protocol.
func TestSignInCompletionContextCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, legacy, current string }{
		{"new_only", "", "stable-instance"},
		{"legacy_fallback", "stable-instance", ""},
		{"new_precedence", "different-legacy-instance", "stable-instance"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			d := f.deps
			d.IdentityFlowContext = "stable-instance"
			var err error
			f.svc, err = user.NewService(d)
			if err != nil {
				t.Fatal(err)
			}
			p := pendingFlow(t, f, "context-compatibility")
			cred := completionCredential(t, f, p, enum.IdentityEmail, email1)
			var id string
			var ciphertext []byte
			var keyVersion uint16
			if err := f.pool.QueryRow(ctx, `SELECT id,payload,cipher_version FROM sign_in_completion_flow`).Scan(&id, &ciphertext, &keyVersion); err != nil {
				t.Fatal(err)
			}
			searchPath, _, _ := strings.Cut(f.pool.Config().ConnConfig.RuntimeParams["search_path"], ",")
			searchPath = strings.Trim(strings.TrimSpace(searchPath), `"`)
			// This explicit legacy wire document is deliberately independent of the
			// implementation's fingerprint helper and struct field names.
			legacyJSON := fmt.Sprintf(`{"SearchPath":%q,"Prefix":%q,"JWT":%q,"Extra":"stable-instance"}`, searchPath, d.Codes.InstancePrefix(), d.Signer.ContextFingerprint())
			fingerprint := sha256.Sum256([]byte(legacyJSON))
			aad, err := json.Marshal([]string{"accountkit-sign-in-completion-v1", fmt.Sprintf("%x", fingerprint), id})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.ciph.DecryptWithAAD(ciphertext, keyVersion, aad); err != nil {
				t.Fatalf("legacy context serialization or AAD changed: %v", err)
			}
			d.IdentityFlowContext, d.SignInCompletionFlowContext = tc.legacy, tc.current
			current, err := user.NewService(d)
			if err != nil {
				t.Fatal(err)
			}
			result, err := current.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil {
				t.Fatalf("old encrypted ticket cannot complete with new configuration: %v", err)
			}
			pair := requireSignInTokens(t, result)
			replay, err := current.CompleteSignIn(ctx, p.SignInCompletionToken, cred, user.Device{}, meta1)
			if err != nil || requireSignInTokens(t, replay) != pair {
				t.Fatalf("completed ciphertext replay changed: %v", err)
			}
		})
	}
}
