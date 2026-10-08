package accountkit_test

import (
	"context"
	"testing"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deliberately equal source IDs and refresh tokens expose missing Redis/schema namespaces.
func TestIndependentInstances(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	ca, cb := minimal(), minimal()
	ca.KeyPrefix = "product_a:"
	cb.KeyPrefix = "product_b:"
	ca.JWTIssuer = "a"
	cb.JWTIssuer = "b"
	ca.JWTAudience = "a-app"
	cb.JWTAudience = "b-app"
	cb.JWTKeys = map[uint16][]byte{1: k(9)}
	a, pa, sa := integrationInstance(t, ca, rdb)
	b, pb, sb := integrationInstance(t, cb, rdb)
	fa, accessA := seedSourceFixture(t, pa, a.Config())
	fb, accessB := seedSourceFixture(t, pb, b.Config())
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := b.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Users().Authenticate(ctx, accessB); err == nil {
		t.Fatal("A accepted B access")
	}
	if _, err := b.Users().Authenticate(ctx, accessA); err == nil {
		t.Fatal("B accepted A access")
	}
	meta := user.Meta{IP: "127.0.0.1"}
	ta, err := a.Users().Refresh(ctx, fa.RefreshToken, meta)
	if err != nil {
		t.Fatal(err)
	}
	tb, err := b.Users().Refresh(ctx, fb.RefreshToken, meta)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := a.Users().Refresh(ctx, fa.RefreshToken, meta)
	if err != nil || replay.RefreshToken != ta.RefreshToken {
		t.Fatalf("grace namespace: %v", err)
	}
	if err := a.Users().Revoke(ctx, ta.RefreshToken, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Users().Authenticate(ctx, ta.AccessToken); err == nil {
		t.Fatal("A revoke did not take effect")
	}
	if _, err := b.Users().Authenticate(ctx, tb.AccessToken); err != nil {
		t.Fatalf("A revoked B: %v", err)
	}
	var challengeA, challengeB user.CodeChallenge
	for _, x := range []struct {
		send func() error
		code func() string
	}{
		{func() error {
			var err error
			challengeA, err = a.Users().SendSignInCode(ctx, enum.IdentityPhone, fa.Phone, meta)
			return err
		}, func() string { return sa.code(fa.Phone) }},
		{func() error {
			var err error
			challengeB, err = b.Users().SendSignInCode(ctx, enum.IdentityPhone, fb.Phone, meta)
			return err
		}, func() string { return sb.code(fb.Phone) }},
	} {
		if err := x.send(); err != nil {
			t.Fatal(err)
		}
		if x.code() == "" {
			t.Fatal("code not sent")
		}
	}
	// Consuming A's OTP must not consume B's, even with equal HMAC keys and target.
	for _, x := range []struct {
		signIn func() (user.TokenResult, error)
	}{
		{func() (user.TokenResult, error) {
			return a.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: fa.Phone, CodeID: challengeA.CodeID, Code: sa.code(fa.Phone)}, user.Device{ID: "otp-device"}, meta)
		}},
		{func() (user.TokenResult, error) {
			return b.Users().SignInWithCode(ctx, user.CodeCredential{Channel: enum.IdentityPhone, Target: fb.Phone, CodeID: challengeB.CodeID, Code: sb.code(fb.Phone)}, user.Device{ID: "otp-device"}, meta)
		}},
	} {
		if _, err := x.signIn(); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []*pgxpool.Pool{pa, pb} {
		if _, err := p.Exec(ctx, "UPDATE session SET revoke_time=now()-interval '40 days' WHERE id=$1", fa.SessionID); err != nil {
			t.Fatal(err)
		}
	}
	if !a.RunMaintenanceOnce(ctx) {
		t.Fatal("maintenance lock not acquired")
	}
	var na, nb int
	if err := pa.QueryRow(ctx, "SELECT count(*) FROM session WHERE id=$1", fa.SessionID).Scan(&na); err != nil {
		t.Fatal(err)
	}
	if err := pb.QueryRow(ctx, "SELECT count(*) FROM session WHERE id=$1", fb.SessionID).Scan(&nb); err != nil {
		t.Fatal(err)
	}
	if na != 0 || nb != 1 {
		t.Fatalf("maintenance crossed schema: A=%d B=%d", na, nb)
	}
}
