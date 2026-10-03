package idp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/user/idp"
)

func TestNonceRegistryRegisterOnce(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	reg := idp.NewNonceRegistry(rdb, "t:")
	ctx := context.Background()

	if err := reg.Register(ctx, "abc123", 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("abc123"))
	key := "t:nonce:apple:" + hex.EncodeToString(sum[:])
	if !mr.Exists(key) {
		t.Fatalf("key %s must exist", key)
	}
	if ttl := mr.TTL(key); ttl <= 9*time.Minute || ttl > 10*time.Minute {
		t.Fatalf("ttl %v", ttl)
	}
	if err := reg.Register(ctx, "abc123", 10*time.Minute); !errors.Is(err, idp.ErrNonceReplayed) {
		t.Fatalf("replay: %v", err)
	}
	if err := reg.Register(ctx, "other", 10*time.Minute); err != nil {
		t.Fatalf("different nonce: %v", err)
	}
	mr.FastForward(10*time.Minute + time.Second)
	if err := reg.Register(ctx, "abc123", 10*time.Minute); err != nil {
		t.Fatalf("after ttl the nonce may be registered again (id_token itself is long expired): %v", err)
	}
}

// Register 的 ttl 由调用方按 id_token 剩余有效期计算给出；一个更长的 ttl 必须真正
// 延长 Redis 里的过期时间，覆盖住整个 id_token 生命周期（C1）。
func TestNonceRegistryRegisterHonorsCallerTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	reg := idp.NewNonceRegistry(rdb, "t:")
	ctx := context.Background()

	if err := reg.Register(ctx, "long-lived", 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("long-lived"))
	key := "t:nonce:apple:" + hex.EncodeToString(sum[:])
	if ttl := mr.TTL(key); ttl < 24*time.Hour {
		t.Fatalf("ttl %v must cover the caller-supplied 24h", ttl)
	}
}

func TestNonceRegistryRejectsNonPositiveTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	reg := idp.NewNonceRegistry(rdb, "t:")
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := reg.Register(context.Background(), "x", ttl); !errors.Is(err, idp.ErrMisconfigured) {
			t.Fatalf("ttl %v: got %v want ErrMisconfigured", ttl, err)
		}
	}
}

func TestNonceRegistryRedisDownIsUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	reg := idp.NewNonceRegistry(rdb, "t:")
	mr.Close()
	if err := reg.Register(context.Background(), "x", time.Minute); !errors.Is(err, idp.ErrUnavailable) {
		t.Fatalf("redis down: %v", err)
	}
}

func TestNonceRegistryRejectsEmpty(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	reg := idp.NewNonceRegistry(rdb, "t:")
	if err := reg.Register(context.Background(), "", time.Minute); !errors.Is(err, idp.ErrInvalidCredential) {
		t.Fatalf("empty nonce: %v", err)
	}
}
