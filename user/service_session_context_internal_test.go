package user

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/jackc/pgx/v5/pgconn"
)

// A malformed internal reference must fail before changing the previous source
// set, even though this schema intentionally has no foreign keys.
func TestSessionAuthenticationReferences(t *testing.T) {
	pool := profilesPool(t, &profilesTrace{})
	repo := NewRepo(pool)
	ctx := context.Background()
	uid, _ := ids.New(ids.User)
	otherUID, _ := ids.New(ids.User)
	for _, id := range []string{uid, otherUID} {
		if _, err := repo.queries().CreateUser(ctx, db.CreateUserParams{ID: id, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
	}
	create := func(owner string, kind enum.IdentityKind) string {
		id, _ := ids.New(ids.Identity)
		subject := id
		if _, err := repo.queries().CreateIdentity(ctx, db.CreateIdentityParams{ID: id, UserID: owner, Kind: kind, ProviderSubject: &subject}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	valid := create(uid, enum.IdentityWeChat)
	foreign := create(otherUID, enum.IdentityWeChat)
	deleted := create(uid, enum.IdentityWeChat)
	disabled := create(uid, enum.IdentityApple)
	if _, err := repo.queries().SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: deleted, UserID: uid, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, _ := ids.New(ids.Session)
	_, err := repo.queries().CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: "reference-test", AuthTime: time.Now(), RefreshTokenHash: make([]byte, 32), RefreshExpireTime: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewIdentityPolicy([]enum.IdentityKind{enum.IdentityWeChat}, []enum.IdentityKind{})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{d: Deps{Now: time.Now}, policy: policy}
	set := func(ids []string) error {
		return repo.withTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockUserByID(ctx, uid); err != nil {
				return err
			}
			sess, err := q.LockSessionByID(ctx, sid)
			if err != nil {
				return err
			}
			return svc.setSessionAuthenticationIdentities(ctx, q, sess, ids)
		})
	}
	if err := set([]string{valid, valid}); err != nil {
		t.Fatal("deduplicated valid reference:", err)
	}
	for name, input := range map[string][]string{"empty": nil, "foreign": {foreign}, "missing": {"i_0000000000000"}, "deleted": {deleted}, "disabled": {disabled}, "partial valid invalid": {valid, foreign}} {
		t.Run(name, func(t *testing.T) {
			if err := set(input); err == nil {
				t.Fatal("invalid authentication identity accepted")
			}
			rows, err := repo.queries().ListSessionAuthenticationIdentities(ctx, db.ListSessionAuthenticationIdentitiesParams{UserID: uid, SessionID: sid})
			if err != nil || len(rows) != 1 || rows[0].IdentityID != valid {
				t.Fatalf("invalid source changed previous set: %+v %v", rows, err)
			}
		})
	}
}

func TestCurrentSessionContextStorageErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		cause       error
		unavailable bool
	}{
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"wrapped EOF", fmt.Errorf("read current session: %w", io.EOF), true},
		{"wrapped unexpected EOF", fmt.Errorf("read current session: %w", io.ErrUnexpectedEOF), true},
		{"SQL programming error", &pgconn.PgError{Code: "42703", Message: "synthetic undefined column"}, false},
		{"wrapped SQL programming error", fmt.Errorf("read current session: %w", &pgconn.PgError{Code: "42P01", Message: "synthetic undefined table"}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionContextStorageError(tc.cause)
			if errors.Is(got, ErrUnavailable) != tc.unavailable {
				t.Errorf("dependency classification=%v want unavailable=%v", got, tc.unavailable)
			}
			if errors.Is(got, ErrInvalidToken) {
				t.Error("storage failure classified as invalid token")
			}
			if !errors.Is(got, tc.cause) {
				t.Error("storage cause lost")
			}
			if !tc.unavailable && got != tc.cause {
				t.Error("programming error must remain unchanged")
			}
		})
	}
}
