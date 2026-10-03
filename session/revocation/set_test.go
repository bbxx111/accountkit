package revocation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/session/revocation"
)

func newSet(t *testing.T) (*revocation.Set, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return revocation.NewSet(rdb, "t:"), mr
}

func TestRevokeThenIsRevokedThenExpires(t *testing.T) {
	s, mr := newSet(t)
	ctx := context.Background()
	if r, err := s.IsRevoked(ctx, "s_0123456789abc"); err != nil || r {
		t.Fatalf("fresh sid must not be revoked: %v %v", r, err)
	}
	if err := s.Revoke(ctx, "s_0123456789abc", 15*time.Minute); err != nil {
		t.Fatal(err)
	}
	if r, err := s.IsRevoked(ctx, "s_0123456789abc"); err != nil || !r {
		t.Fatalf("must be revoked: %v %v", r, err)
	}
	if !mr.Exists("t:revoked:s_0123456789abc") {
		t.Fatal("key must carry the configured prefix")
	}
	mr.FastForward(16 * time.Minute)
	if r, _ := s.IsRevoked(ctx, "s_0123456789abc"); r {
		t.Fatal("entry must expire with the TTL")
	}
}

func TestRedisDownReturnsUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	s := revocation.NewSet(rdb, "t:")
	mr.Close()
	if _, err := s.IsRevoked(context.Background(), "s_x"); !errors.Is(err, revocation.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Revoke(context.Background(), "s_x", time.Minute); !errors.Is(err, revocation.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestRevokeRejectsNonPositiveTTL(t *testing.T) {
	s, mr := newSet(t)
	if err := s.Revoke(context.Background(), "s_zero", 0); err == nil {
		t.Fatal("zero ttl must be rejected")
	}
	if err := s.Revoke(context.Background(), "s_neg", -time.Second); err == nil {
		t.Fatal("negative ttl must be rejected")
	}
	if mr.Exists("t:revoked:s_zero") || mr.Exists("t:revoked:s_neg") {
		t.Fatal("rejected ttl must not write a key")
	}
}
