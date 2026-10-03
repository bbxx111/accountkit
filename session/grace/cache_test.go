package grace_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/pii"
	"github.com/bbxx111/accountkit/session/grace"
)

func newCache(t *testing.T) (*grace.Cache, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	c, err := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{3}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return grace.NewCache(rdb, "t:", c), mr, rdb
}

func samplePair() grace.Pair {
	now := time.Now().Truncate(time.Second)
	return grace.Pair{AccessToken: "acc.ess.token", RefreshToken: "refresh-token-value", AccessExpiresAt: now.Add(15 * time.Minute), RefreshExpiresAt: now.Add(720 * time.Hour), Scope: "user"}
}

func TestPutGetRoundTripEncryptedAndExpires(t *testing.T) {
	c, mr, rdb := newCache(t)
	ctx := context.Background()
	h := sha256.Sum256([]byte("old-refresh"))
	p := samplePair()
	if err := c.Put(ctx, h[:], p, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(ctx, h[:])
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.AccessToken != p.AccessToken || got.RefreshToken != p.RefreshToken || !got.RefreshExpiresAt.Equal(p.RefreshExpiresAt) || got.Scope != "user" {
		t.Fatalf("pair mismatch: %+v", got)
	}
	// 值必须是密文：原文 token 不能在 Redis 里直接出现
	keys, _ := rdb.Keys(ctx, "t:grace:*").Result()
	if len(keys) != 1 {
		t.Fatalf("keys = %v", keys)
	}
	raw, _ := rdb.Get(ctx, keys[0]).Bytes()
	if bytes.Contains(raw, []byte("refresh-token-value")) {
		t.Fatal("stored value must be encrypted")
	}
	mr.FastForward(31 * time.Second)
	if _, ok, err := c.Get(ctx, h[:]); err != nil || ok {
		t.Fatalf("must expire: ok=%v err=%v", ok, err)
	}
}

func TestGetMissAndTamperedValue(t *testing.T) {
	c, _, rdb := newCache(t)
	ctx := context.Background()
	h := sha256.Sum256([]byte("nothing"))
	if _, ok, err := c.Get(ctx, h[:]); err != nil || ok {
		t.Fatalf("miss: ok=%v err=%v", ok, err)
	}
	_ = c.Put(ctx, h[:], samplePair(), time.Minute)
	keys, _ := rdb.Keys(ctx, "t:grace:*").Result()
	_ = rdb.Set(ctx, keys[0], "garbage", time.Minute).Err()
	if _, ok, err := c.Get(ctx, h[:]); ok || err == nil {
		t.Fatalf("tampered value must be a miss with an error: ok=%v err=%v", ok, err)
	}
}

func TestRedisDown(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 200 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })
	cipher, err := pii.NewCipher(map[uint16][]byte{1: bytes.Repeat([]byte{3}, 32)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	c := grace.NewCache(rdb, "t:", cipher)
	mr.Close()
	h := sha256.Sum256([]byte("x"))
	if err := c.Put(context.Background(), h[:], samplePair(), time.Minute); !errors.Is(err, grace.ErrUnavailable) {
		t.Fatalf("put: %v", err)
	}
	if _, _, err := c.Get(context.Background(), h[:]); !errors.Is(err, grace.ErrUnavailable) {
		t.Fatalf("get: %v", err)
	}
}

func TestPutRejectsNonPositiveTTL(t *testing.T) {
	c, mr, _ := newCache(t)
	ctx := context.Background()
	h := sha256.Sum256([]byte("zero-ttl"))
	if err := c.Put(ctx, h[:], samplePair(), 0); err == nil {
		t.Fatal("zero ttl must be rejected")
	}
	if err := c.Put(ctx, h[:], samplePair(), -time.Second); err == nil {
		t.Fatal("negative ttl must be rejected")
	}
	for _, k := range mr.Keys() {
		if bytes.HasPrefix([]byte(k), []byte("t:grace:")) {
			t.Fatalf("rejected ttl must not write a key, found %s", k)
		}
	}
}
