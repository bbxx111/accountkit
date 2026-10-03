package user

import (
	"errors"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/user/db"
)

func TestSessionActiveAt(t *testing.T) {
	at := time.Date(2030, 1, 2, 3, 4, 5, 123456000, time.UTC)
	revoked := at.Add(-time.Hour)
	for _, tc := range []struct {
		name     string
		expiry   time.Time
		decision time.Time
		revoke   *time.Time
		want     bool
	}{
		{"before_expiry", at.Add(time.Microsecond), at, nil, true},
		{"at_expiry", at, at, nil, false},
		{"after_expiry", at.Add(-time.Microsecond), at, nil, false},
		{"revoked", at.Add(time.Hour), at, &revoked, false},
		{"timezone", at.Add(time.Microsecond), at.In(time.FixedZone("UTC+8", 8*3600)), nil, true},
		{"submicrosecond_before", at.Add(time.Microsecond), at.Add(999 * time.Nanosecond), nil, true},
		{"submicrosecond_at", at, at.Add(999 * time.Nanosecond), nil, false},
		{"next_microsecond", at.Add(time.Microsecond), at.Add(time.Microsecond), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionActiveAt(db.Session{RefreshExpireTime: tc.expiry, RevokeTime: tc.revoke}, tc.decision); got != tc.want {
				t.Errorf("active = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("time_normalization", func(t *testing.T) {
		got := sessionTime(at.In(time.FixedZone("UTC-7", -7*3600)).Add(999 * time.Nanosecond))
		if got != at || got.Location() != time.UTC {
			t.Errorf("normalized = %s (%s), want UTC %s", got, got.Location(), at)
		}
	})
	t.Run("expired_error_preserves_public_classification", func(t *testing.T) {
		if !errors.Is(errSessionExpired, ErrInvalidToken) {
			t.Fatal("expired session must preserve ErrInvalidToken classification")
		}
	})
}
