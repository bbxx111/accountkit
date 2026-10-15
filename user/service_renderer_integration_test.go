package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
	delivery "github.com/bbxx111/accountkit/user/sender/smtp"
)

type integrationRenderFunc func(context.Context, sender.CodeMessage) (delivery.EmailContent, error)

func (f integrationRenderFunc) Render(ctx context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
	return f(ctx, m)
}

// Exercise the public HTTP → Service → real SMTP adapter path, including a persisted,
// still-valid completion flow. The renderer fails before any SMTP transport.
func TestSMTPRendererFailureHTTPAgainstRealDB(t *testing.T) {
	for _, purpose := range []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete} {
		t.Run(purpose.String(), func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pair := f.signIn(t, enum.IdentityEmail, email1, dev1)
			principal := principalOf(t, f, pair)
			path := map[enum.CodePurpose]string{
				enum.PurposeSignIn: "/users:sendSignInCode", enum.PurposeBind: "/users/me:sendBindCode",
				enum.PurposeReauth: "/users/me:sendReauthenticationCode", enum.PurposeSignInComplete: "/users:sendSignInCompletionCode",
			}[purpose]
			request := map[string]string{"channel": "EMAIL", "target": email1}
			var flowID string
			var flowExpiry time.Time
			if purpose == enum.PurposeSignInComplete {
				pending := pendingFlow(t, f, "render-failure")
				request["sign_in_completion_token"] = pending.SignInCompletionToken
				flowExpiry = pending.ExpireTime
				f.advance(flowExpiry.Sub(*f.clock) - 90*time.Second)
				if err := f.pool.QueryRow(ctx, "SELECT id FROM identity_flow").Scan(&flowID); err != nil {
					t.Fatal(err)
				}
			}
			start := len(f.audit.Events())
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			var codeID, plain string
			calls := 0
			transport, err := delivery.NewWithRenderer(delivery.Config{Host: "127.0.0.1", Port: 1, From: "sender@example.org", Timeout: time.Second},
				integrationRenderFunc(func(_ context.Context, m sender.CodeMessage) (delivery.EmailContent, error) {
					calls++
					plain = m.Code
					if m.Purpose != purpose || m.TTL <= 0 {
						t.Error("wrong public purpose or TTL")
					}
					if purpose != enum.PurposeSignInComplete && m.TTL != 5*time.Minute {
						t.Error("ordinary code TTL changed")
					}
					for _, key := range f.mr.Keys() {
						if !strings.Contains(key, ":challenge:") {
							continue
						}
						codeID = f.mr.HGet(key, "code_id")
						if purpose == enum.PurposeBind || purpose == enum.PurposeReauth {
							if f.mr.HGet(key, "user_id") != principal.UserID {
								t.Error("code not bound to principal")
							}
						}
						if purpose == enum.PurposeReauth && f.mr.HGet(key, "session_id") != principal.SessionID {
							t.Error("reauth lost session binding")
						}
						if purpose == enum.PurposeSignInComplete {
							millis, err := strconv.ParseInt(f.mr.HGet(key, "expire_time"), 10, 64)
							if err != nil || f.mr.HGet(key, "flow_id") != flowID || time.UnixMilli(millis).After(flowExpiry) || m.TTL > 90*time.Second {
								t.Error("completion lost original flow/deadline")
							}
						}
					}
					return delivery.EmailContent{}, fmt.Errorf("private-template %s %s body language-context", email1, m.Code)
				}))
			if err != nil {
				t.Fatal(err)
			}
			newServiceWith(t, f, func(d *user.Deps) { d.Email = transport; d.Logger = logger })
			handler, err := enduser.New(enduser.Deps{Users: f.svc, Logger: logger, Now: f.deps.Now, ReauthMaxAge: 5 * time.Minute,
				ClientIP: func(*http.Request) string { return meta1.IP }, RequestID: func(*http.Request) string { return "render-http" }})
			if err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if purpose == enum.PurposeBind || purpose == enum.PurposeReauth {
				req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
			}
			rec := httptest.NewRecorder()
			handler.Router().ServeHTTP(rec, req)
			if rec.Code != 503 || !strings.Contains(rec.Body.String(), "DEPENDENCY_UNAVAILABLE") || rec.Header().Get("WWW-Authenticate") != "" {
				t.Fatal("render failure HTTP mapping changed", rec.Code, rec.Body.String())
			}
			if calls != 1 || codeID == "" || strings.Contains(rec.Body.String(), "code_id") || strings.Contains(rec.Body.String(), "expire_time") {
				t.Fatal("render failure returned success or retried")
			}
			for _, key := range f.mr.Keys() {
				if strings.Contains(key, ":challenge:") {
					t.Fatal("failed rendering left usable round")
				}
			}
			events := f.audit.Events()[start:]
			found := false
			for _, event := range events {
				if event.Type == enum.EventCodeSendFailed && event.Reason == "SEND_FAILED" {
					found = true
					if event.FlowID != flowID || event.SubjectHint == "" {
						t.Fatal("failure audit lost flow or redaction")
					}
				}
			}
			if !found {
				t.Fatal("missing render failure audit")
			}
			for _, secret := range []string{email1, plain, "private-template", "language-context", request["sign_in_completion_token"]} {
				if secret != "" && (strings.Contains(logs.String(), secret) || strings.Contains(fmt.Sprint(events), secret) || strings.Contains(rec.Body.String(), secret)) {
					t.Fatal("HTTP rendering failure leaked sensitive data")
				}
			}
			// A second request keeps the consumed cooldown, without calling Render again.
			again := httptest.NewRecorder()
			retry := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			retry.Header = req.Header.Clone()
			handler.Router().ServeHTTP(again, retry)
			if again.Code != 429 || calls != 1 {
				t.Fatal("HTTP render failure refunded cooldown", again.Code, again.Body.String())
			}
		})
	}
}
