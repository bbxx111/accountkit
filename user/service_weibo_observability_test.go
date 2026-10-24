package user_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	ob "github.com/bbxx111/accountkit/observability"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestWeiboProviderObservationsAndFlowFingerprint(t *testing.T) {
	for _, scenario := range []string{"missing fields", "degraded", "frozen", "pending", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			f, r := observedFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var requests atomic.Int32
			var armed atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if req.URL.Path == "/oauth2/get_token_info" {
					fmt.Fprint(w, `{"uid":2000000001,"appkey":"1234567890","expire_in":3600}`)
					return
				}
				if armed.Load() && scenario == "canceled" {
					cancel()
					<-req.Context().Done()
					return
				}
				if armed.Load() && scenario == "degraded" {
					w.WriteHeader(403)
					fmt.Fprint(w, `{"error":"User requests out of rate limit!","error_code":10023}`)
					return
				}
				fmt.Fprint(w, `{"id":2000000001}`)
			}))
			defer srv.Close()
			cfg := idp.WeiboConfig{AppIDs: []string{"1234567890"}, APIBaseURL: srv.URL}
			provider, err := idp.NewWeibo(idp.WeiboOptions{Config: cfg, Observer: r, Logger: f.deps.Logger})
			if err != nil {
				t.Fatal(err)
			}
			cred := providerCred(enum.IdentityWeibo, map[string]string{"app_id": "1234567890", "access_token": "synthetic-token"})
			f.deps = withProvider(f.deps, provider)
			f.deps.RequiredIdentityKinds = []enum.IdentityKind{}
			if scenario == "pending" {
				f.deps.RequiredIdentityKinds = nil
			}
			rebuildMetricsService(t, f)
			if scenario == "frozen" {
				first := requireSignInTokens(t, mustSignInWithIdp(t, f, cred))
				mustExec(t, f, `UPDATE user_account SET state=$2 WHERE id=$1`, first.UserID, enum.UserFrozen)
				r.Reset()
				requests.Store(0)
			}
			armed.Store(true)
			result, err := f.svc.SignInWithIdp(ctx, cred, dev1, meta1)
			want := ob.ResultSuccess
			switch scenario {
			case "frozen":
				want = ob.ResultRejected
				if !errors.Is(err, user.ErrUserFrozen) {
					t.Fatal(err)
				}
			case "canceled":
				want = ob.ResultCanceled
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "pending":
				want = ob.ResultPending
				if err != nil || result.Pending == nil {
					t.Fatal("expected pending", err)
				}
			default:
				if err != nil || result.Tokens == nil {
					t.Fatal("expected successful sign-in", err)
				}
			}
			deps, auth := dependencyValues(r, ob.ComponentIDP, ob.DependencyVerifyWeibo), authValues(r)
			depWant := ob.ResultSuccess
			if scenario == "canceled" {
				depWant = ob.ResultCanceled
			}
			if len(deps) != 1 || deps[0].Result != depWant || len(auth) != 1 || auth[0].Method != ob.MethodWeibo || auth[0].Result != want || requests.Load() != 2 {
				t.Fatalf("deps=%+v auth=%+v requests=%d", deps, auth, requests.Load())
			}
			var degradations int64
			for _, v := range r.Snapshot() {
				if d, ok := v.(ob.DegradationObservation); ok && d.Scenario == ob.DegradationWeiboProfileUnavailable {
					degradations += d.Count
				}
			}
			wantDegradations := int64(0)
			if scenario == "degraded" {
				wantDegradations = 1
			}
			if degradations != wantDegradations {
				t.Fatalf("degradation=%d want=%d", degradations, wantDegradations)
			}
			if scenario == "canceled" {
				var n int
				if err := f.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_account`).Scan(&n); err != nil || n != 0 {
					t.Fatal("canceled verification wrote an account")
				}
			}
			if scenario == "pending" {
				cfg.AppIDs = append(cfg.AppIDs, "1234567891")
				changed, err := idp.NewWeibo(idp.WeiboOptions{Config: cfg})
				if err != nil {
					t.Fatal(err)
				}
				f.deps = withProvider(f.deps, changed)
				rebuildMetricsService(t, f)
				if _, err := f.svc.SendSignInCompletionCode(context.Background(), result.Pending.SignInCompletionToken, enum.IdentityEmail, email1, dev1, meta1); !errors.Is(err, user.ErrSignInCompletionInvalid) {
					t.Fatalf("policy fingerprint change accepted: %v", err)
				}
			}
		})
	}
}
