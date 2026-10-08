package code_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
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
			if _, err := f.stores[1].IssueChallenge(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); !errors.Is(err, code.ErrUnavailable) {
				t.Error("issue did not fail closed")
			}
			if err := f.stores[1].VerifyChallenge(context.Background(), credential(code.Issued{CodeID: strings.Repeat("a", 32), Code: "synthetic-fixture"}, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrUnavailable) {
				t.Error("verify did not fail closed")
			}
		})
	}
	t.Run("redis down", func(t *testing.T) {
		const completed = "accountkit Redis disconnect probe completed"
		// go-redis has a process-wide logger. Capture this transport-failure
		// probe in a child process rather than changing other tests' logging.
		if os.Getenv("ACCOUNTKIT_CODE_DISCONNECT_HELPER") != "1" {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCodeRotationUnavailable$/^redis_down$", "-test.count=1")
			cmd.Env = append(os.Environ(), "ACCOUNTKIT_CODE_DISCONNECT_HELPER=1")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("Redis disconnect probe failed: %v\n%s", err, output)
			}
			if !strings.Contains(string(output), completed+"\n") {
				t.Fatalf("Redis disconnect probe did not execute:\n%s", output)
			}
			for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
				line = strings.TrimSpace(line)
				if line != "PASS" && line != completed && !strings.Contains(line, "redis: connection pool: failed to dial after") {
					t.Log(line) // Keep unexpected diagnostics visible.
				}
			}
			return
		}
		f := newRotation(t, false)
		f.mr.Close()
		if _, err := f.stores[1].IssueChallenge(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); !errors.Is(err, code.ErrUnavailable) {
			t.Error("challenge issue did not fail closed")
		}
		if err := f.stores[1].VerifyChallenge(context.Background(), code.Credential{Channel: enum.IdentityPhone, Purpose: enum.PurposeSignIn, Target: target, CodeID: strings.Repeat("a", 32), Code: "synthetic-fixture"}); !errors.Is(err, code.ErrUnavailable) {
			t.Error("challenge verify did not fail closed")
		}
		if err := f.stores[1].DiscardChallenge(context.Background(), enum.IdentityPhone, enum.PurposeSignIn, target, strings.Repeat("a", 32)); !errors.Is(err, code.ErrUnavailable) {
			t.Error("challenge discard did not fail closed")
		}
		fmt.Println(completed)
	})
}

func TestChallengeUnavailable(t *testing.T) {
	for _, res := range []interface{}{nil, []interface{}{}, []interface{}{"OK"}, []interface{}{"OK", ""}, []interface{}{"MISSING", int64(1)}, []interface{}{"TARGET_VERIFY_LIMIT", int64(0)}, []interface{}{"TARGET_VERIFY_LIMIT", int64(-1)}, []interface{}{"UNKNOWN", int64(0)}} {
		f := challengeFixture(t, false)
		c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
		f.rdb.AddHook(scriptResponseHook{result: res})
		if err := f.stores[0].VerifyChallenge(context.Background(), c); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("malformed verify response accepted")
		}
		if _, err := f.stores[0].IssueChallenge(context.Background(), c.Channel, c.Purpose, c.Target, "test-ip", c.Binding); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("malformed issue response accepted")
		}
		if err := f.stores[0].DiscardChallenge(context.Background(), c.Channel, c.Purpose, c.Target, c.CodeID); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("malformed discard response accepted")
		}
	}
}

func TestChallengeUnexpectedOperationResponse(t *testing.T) {
	for _, op := range []string{"issue", "verify", "discard"} {
		t.Run(op, func(t *testing.T) {
			f := challengeFixture(t, false)
			c := issueChallenge(t, f, 0, enum.PurposeSignIn, code.Binding{})
			response := []interface{}{"MISSING", int64(0)}
			if op == "verify" {
				response = []interface{}{"COOLDOWN", int64(1)}
			}
			f.rdb.AddHook(scriptResponseHook{result: response})
			var err error
			switch op {
			case "issue":
				_, err = f.stores[0].IssueChallenge(context.Background(), c.Channel, c.Purpose, c.Target, "test-ip", c.Binding)
			case "verify":
				err = f.stores[0].VerifyChallenge(context.Background(), c)
			case "discard":
				err = f.stores[0].DiscardChallenge(context.Background(), c.Channel, c.Purpose, c.Target, c.CodeID)
			}
			if !errors.Is(err, code.ErrUnavailable) {
				t.Fatalf("unexpected script status: %v", err)
			}
		})
	}
}

func TestCodeRotationCorruptState(t *testing.T) {
	ctx := context.Background()
	t.Run("cooldown without ttl", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.Set(f.key(0, "cooldown", enum.IdentityPhone, enum.PurposeSignIn), "1")

		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("persistent cooldown not rejected")
		}
	})
	t.Run("wrong quota type", func(t *testing.T) {
		f := newRotation(t, false)
		f.mr.HSet(f.key(0, "quota", enum.IdentityPhone, enum.PurposeSignIn), "n", "1")
		if _, err := f.stores[1].IssueChallenge(ctx, enum.IdentityPhone, enum.PurposeSignIn, target, "test-ip", code.Binding{}); !errors.Is(err, code.ErrUnavailable) {
			t.Fatal("invalid quota not rejected")
		}
	})
	for _, kind := range []string{"no ttl", "wrong type", "missing count"} {
		t.Run(kind, func(t *testing.T) {
			f := newRotation(t, false)
			plain := f.priorChallenge(t, 0, 0, 0)
			key := f.key(0, "challenge", enum.IdentityPhone, enum.PurposeSignIn)
			switch kind {
			case "wrong type":
				f.mr.Del(key)
				f.mr.Set(key, "corrupt")
				f.mr.SetTTL(key, time.Minute)
			case "missing count":
				f.mr.HDel(key, "n")
				f.mr.SetTTL(key, time.Minute)
			}
			if err := f.stores[1].VerifyChallenge(ctx, credential(plain, enum.IdentityPhone, enum.PurposeSignIn, target)); !errors.Is(err, code.ErrUnavailable) {
				t.Fatal("invalid code state not rejected")
			}
		})
	}
}
