package accountkit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
)

// 使用已取消的 ctx 和不连接的池：只观察配置是否使领域预检正确拒绝或继续，New 仍无需 I/O。
func TestNewWiresIdentityReplacementConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		age        time.Duration
		enabled    *bool
		authAge    time.Duration
		wantReauth bool
	}{
		{"default", 0, nil, 6 * time.Minute, true},
		{"short-window", time.Minute, nil, 2 * time.Minute, true},
		{"long-window", 10 * time.Minute, nil, 6 * time.Minute, false},
		{"disabled", time.Minute, replacementConfigBool(false), time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimal()
			cfg.ReauthMaxAge = tc.age
			cfg.SensitiveOpVerification = tc.enabled
			deps := baseDeps(t)
			deps.Audit = audit.Noop{}
			a, err := accountkit.New(cfg, deps)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			p := user.Principal{UserID: "u_0000000000000", SessionID: "s_0000000000000", Scope: user.ScopeUser, AuthTime: time.Now().Add(-tc.authAge)}
			_, err = a.Users().ReplaceIdentity(ctx, p, "i_0000000000000", enum.IdentityEmail, "new@example.test", "123456", user.Meta{})
			if err == nil || errors.Is(err, user.ErrReauthenticationRequired) != tc.wantReauth {
				t.Fatalf("configuration was not wired: %v wantReauth=%v", err, tc.wantReauth)
			}
		})
	}
}

func replacementConfigBool(v bool) *bool { return &v }
