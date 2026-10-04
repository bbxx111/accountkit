package code_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
)

// Inject only the script response; production mapping still runs unchanged.
type scriptResponseHook struct {
	result interface{}
	err    error
}

func (h scriptResponseHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (h scriptResponseHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h scriptResponseHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "evalsha" || cmd.Name() == "eval" {
			c := cmd.(*redis.Cmd)
			c.SetVal(h.result)
			c.SetErr(h.err)
			return h.err
		}
		return next(ctx, cmd)
	}
}

func TestCodeRotationUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		result interface{}
		err    error
	}{
		{"nil", nil, nil}, {"empty", []interface{}{}, nil}, {"short", []interface{}{"OK"}, nil},
		{"type", []interface{}{int64(1), int64(0)}, nil}, {"unknown", []interface{}{"UNKNOWN", int64(0)}, nil},
		{"extra", []interface{}{"OK", int64(0), int64(0)}, nil},
		{"nil status", []interface{}{nil, int64(0)}, nil},
		{"bad retry", []interface{}{"COOLDOWN", "1"}, nil},
		{"bad unused", []interface{}{"OK", "unexpected"}, nil},
		{"bad daily retry", []interface{}{"TARGET_LIMIT", int64(1)}, nil},
		{"script error", nil, errors.New("synthetic script failure")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRotation(t, false)
			f.rdb.AddHook(scriptResponseHook{tc.result, tc.err})
			defer func() {
				if recover() != nil {
					t.Error("malformed response panicked")
				}
			}()
			if _, err := f.stores[1].Issue(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip"); !errors.Is(err, code.ErrUnavailable) {
				t.Error("issue did not fail closed")
			}
			if err := f.stores[1].Verify(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "synthetic-fixture"); !errors.Is(err, code.ErrUnavailable) {
				t.Error("verify did not fail closed")
			}
		})
	}
	t.Run("redis down", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.Close()
		if _, err := f.stores[1].Issue(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip"); !errors.Is(err, code.ErrUnavailable) {
			t.Error("issue did not fail closed")
		}
		if err := f.stores[1].Verify(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "synthetic-fixture"); !errors.Is(err, code.ErrUnavailable) {
			t.Error("verify did not fail closed")
		}
	})
}

func TestCodeRotationCorruptState(t *testing.T) {
	ctx := context.Background()
	t.Run("cooldown without ttl", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.Set(f.key(0, "cooldown", enum.IdentityPhone, enum.PurposeSignIn), "1")
		if _, err := f.stores[1].Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip"); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("persistent cooldown not rejected")
		}
	})
	t.Run("wrong quota type", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.HSet(f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn), "n", "1")
		if _, err := f.stores[1].Issue(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip"); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("invalid quota not rejected")
		}
	})
	for _, kind := range []string{"no ttl", "wrong type", "missing count"} {
		t.Run(kind, func(t *testing.T) {
			f := newRotation(t, false)
			plain := f.legacy(t, 0, 0, 0)
			key := f.key(0, "code", enum.IdentityPhone, enum.PurposeSignIn)
			switch kind {
			case "wrong type":
				f.mr.Del(key)
				f.mr.Set(key, "corrupt")
				f.mr.SetTTL(key, time.Minute)
			case "missing count":
				f.mr.HDel(key, "n")
				f.mr.SetTTL(key, time.Minute)
			}
			if err := f.stores[1].Verify(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, plain); !errors.Is(err, code.ErrUnavailable) {
				t.Fatal("invalid code state not rejected")
			}
		})
	}
}
