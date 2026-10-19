package user

import (
	"context"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
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
		if _, err := repo.Q().CreateUser(ctx, db.CreateUserParams{ID: id, State: enum.UserActive}); err != nil {
			t.Fatal(err)
		}
	}
	create := func(owner string, kind enum.IdentityKind) string {
		id, _ := ids.New(ids.Identity)
		subject := id
		if _, err := repo.Q().CreateIdentity(ctx, db.CreateIdentityParams{ID: id, UserID: owner, Kind: kind, ProviderSubject: &subject}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	valid := create(uid, enum.IdentityWeChat)
	foreign := create(otherUID, enum.IdentityWeChat)
	deleted := create(uid, enum.IdentityWeChat)
	disabled := create(uid, enum.IdentityApple)
	if _, err := repo.Q().SoftDeleteIdentity(ctx, db.SoftDeleteIdentityParams{ID: deleted, UserID: uid, Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sid, _ := ids.New(ids.Session)
	_, err := repo.Q().CreateSession(ctx, db.CreateSessionParams{ID: sid, UserID: uid, DeviceID: "reference-test", AuthTime: time.Now(), RefreshTokenHash: make([]byte, 32), RefreshExpireTime: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewIdentityPolicy([]enum.IdentityKind{enum.IdentityWeChat}, []enum.IdentityKind{})
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{d: Deps{Now: time.Now}, policy: policy}
	set := func(ids []string) error {
		return repo.WithTx(ctx, func(q *db.Queries) error {
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
			rows, err := repo.Q().ListSessionAuthenticationIdentities(ctx, db.ListSessionAuthenticationIdentitiesParams{UserID: uid, SessionID: sid})
			if err != nil || len(rows) != 1 || rows[0].IdentityID != valid {
				t.Fatalf("invalid source changed previous set: %+v %v", rows, err)
			}
		})
	}
}
