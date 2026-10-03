package user_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
)

type replacementRaceResult struct {
	identity user.IdentityInfo
	token    user.TokenResult
	err      error
}

// 触发器只存在于隔离测试 schema。暂停实际服务事务，同时保留其已取得的行锁。
func replacementMutationGate(t *testing.T, ctx context.Context, f *fixture, table, event, condition string) pgx.Tx {
	t.Helper()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(2147483022)"); err != nil {
		t.Fatal(err)
	}
	mustExec(t, f, `CREATE FUNCTION test_mutation_gate() RETURNS trigger AS $$ BEGIN PERFORM pg_advisory_xact_lock(2147483022); RETURN NEW; END; $$ LANGUAGE plpgsql`)
	mustExec(t, f, fmt.Sprintf(`CREATE TRIGGER test_mutation_gate_trg AFTER %s ON %s FOR EACH ROW WHEN (%s) EXECUTE FUNCTION test_mutation_gate()`, event, table, condition))
	return tx
}

// 返回实际等待者 PID，以便下一请求确定性地等待服务事务而非测试事务。
func replacementWaitForPID(t *testing.T, ctx context.Context, f *fixture, blocker uint32, done <-chan replacementRaceResult) uint32 {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var pid uint32
		err := f.pool.QueryRow(ctx, `SELECT pid FROM pg_stat_activity WHERE $1::int = ANY(pg_blocking_pids(pid)) LIMIT 1`, blocker).Scan(&pid)
		if err == nil {
			return pid
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case out := <-done:
			t.Fatalf("operation completed before expected row-lock wait: %v", out.err)
		case <-ctx.Done():
			t.Fatal("row-lock wait not reached:", ctx.Err())
		case <-tick.C:
		}
	}
}

func startReplacementRace(fn func() replacementRaceResult) <-chan replacementRaceResult {
	done := make(chan replacementRaceResult, 1)
	go func() { done <- fn() }()
	return done
}

func finishReplacementRace(t *testing.T, ctx context.Context, done <-chan replacementRaceResult) replacementRaceResult {
	t.Helper()
	select {
	case out := <-done:
		return out
	case <-ctx.Done():
		t.Fatal("concurrent operations did not finish:", ctx.Err())
		return replacementRaceResult{}
	}
}

func TestReplacementConcurrentMutations(t *testing.T) {
	for _, replacementFirst := range []bool{true, false} {
		order := "replacement-first"
		if !replacementFirst {
			order = "mutation-first"
		}
		for _, operation := range []string{"same-identity", "sign-in", "reauthenticate", "unbind", "freeze", "delete", "logout", "revoke-others", "admin-revoke-all", "refresh-current", "refresh-other"} {
			t.Run(order+"/"+operation, func(t *testing.T) {
				f, p, oldID, current := replacementSetup(t, enum.IdentityPhone, phone1)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				other := f.signIn(t, enum.IdentityPhone, phone1, user.Device{ID: "race-other"})
				otherP, err := f.svc.Authenticate(ctx, other.AccessToken)
				if err != nil {
					t.Fatal(err)
				}
				// 预先轮换其他会话，同时验证被撤销后 current/previous 两种 refresh 均拒绝。
				otherRotated, err := f.svc.Refresh(ctx, other.RefreshToken, meta1)
				if err != nil {
					t.Fatal(err)
				}
				if operation == "unbind" {
					plain := replacementCode(t, f, p, enum.IdentityEmail, email1)
					if _, _, err := f.svc.BindWithCode(ctx, p, enum.IdentityEmail, email1, plain, meta1); err != nil {
						t.Fatal(err)
					}
				}
				newCode := replacementCode(t, f, p, enum.IdentityPhone, phone2)
				var mutationCode string
				const thirdPhone = "+8613900000003"
				switch operation {
				case "same-identity":
					mutationCode = replacementCode(t, f, p, enum.IdentityPhone, thirdPhone)
				case "sign-in":
					if err := f.svc.SendSignInCode(ctx, enum.IdentityPhone, phone1, meta1); err != nil {
						t.Fatal(err)
					}
					mutationCode = f.sent.code(phone1)
				case "reauthenticate":
					if err := f.svc.SendReauthenticationCode(ctx, p, enum.IdentityPhone, phone1, meta1); err != nil {
						t.Fatal(err)
					}
					mutationCode = f.sent.code(phone1)
				}
				replace := func() replacementRaceResult {
					out, err := f.svc.ReplaceIdentity(ctx, p, oldID, enum.IdentityPhone, phone2, newCode, meta1)
					return replacementRaceResult{identity: out, err: err}
				}
				mutate := func() replacementRaceResult {
					var out replacementRaceResult
					switch operation {
					case "same-identity":
						out.identity, out.err = f.svc.ReplaceIdentity(ctx, p, oldID, enum.IdentityPhone, thirdPhone, mutationCode, meta1)
					case "sign-in":
						out.token, out.err = f.svc.SignInWithCode(ctx, enum.IdentityPhone, phone1, mutationCode, user.Device{ID: "race-login"}, meta1)
					case "reauthenticate":
						out.token, out.err = f.svc.Reauthenticate(ctx, p, enum.IdentityPhone, phone1, mutationCode, meta1)
					case "unbind":
						out.err = f.svc.UnbindIdentity(ctx, p, oldID, meta1)
					case "freeze":
						_, out.err = f.svc.Freeze(ctx, user.Admin{Subject: "admin"}, p.UserID, "race", meta1)
					case "delete":
						_, out.err = f.svc.DeleteMe(ctx, p, meta1)
					case "logout":
						out.err = f.svc.Revoke(ctx, current.RefreshToken, meta1)
					case "revoke-others":
						out.err = f.svc.RevokeOtherSessions(ctx, p.UserID, otherP.SessionID, meta1)
					case "admin-revoke-all":
						_, out.err = f.svc.AdminRevokeAllSessions(ctx, user.Admin{Subject: "admin"}, p.UserID, meta1)
					case "refresh-current":
						out.token, out.err = f.svc.Refresh(ctx, current.RefreshToken, meta1)
					case "refresh-other":
						out.token, out.err = f.svc.Refresh(ctx, otherRotated.RefreshToken, meta1)
					}
					return out
				}
				// 换绑先行时，在其他 session 已写入吊销状态而未提交时停下。
				// 另一请求仍可读到旧 identity/session，但后续写锁必须等待换绑。
				table, event := "session", "UPDATE"
				condition := fmt.Sprintf("NEW.id='%s' AND OLD.revoke_time IS NULL AND NEW.revoke_time IS NOT NULL", otherP.SessionID)
				if !replacementFirst {
					switch operation {
					case "same-identity", "unbind":
						table, condition = "identity", fmt.Sprintf("NEW.id='%s' AND NEW.delete_time IS NOT NULL", oldID)
					case "sign-in":
						event, condition = "INSERT", "NEW.device_id='race-login'"
					case "reauthenticate":
						condition = fmt.Sprintf("NEW.id='%s' AND NEW.auth_time IS DISTINCT FROM OLD.auth_time", p.SessionID)
					case "freeze", "delete":
						table, condition = "user_account", "NEW.state IS DISTINCT FROM OLD.state"
					case "logout", "revoke-others", "admin-revoke-all":
						condition = fmt.Sprintf("NEW.id='%s' AND OLD.revoke_time IS NULL AND NEW.revoke_time IS NOT NULL", p.SessionID)
					case "refresh-current":
						condition = fmt.Sprintf("NEW.id='%s' AND NEW.refresh_token_hash IS DISTINCT FROM OLD.refresh_token_hash", p.SessionID)
					case "refresh-other":
						condition = fmt.Sprintf("NEW.id='%s' AND NEW.refresh_token_hash IS DISTINCT FROM OLD.refresh_token_hash", otherP.SessionID)
					}
				}
				gate := replacementMutationGate(t, ctx, f, table, event, condition)
				first, second := replace, mutate
				if !replacementFirst {
					first, second = mutate, replace
				}
				firstDone := startReplacementRace(first)
				firstPID := replacementWaitForPID(t, ctx, f, gate.Conn().PgConn().PID(), firstDone)
				secondDone := startReplacementRace(second)
				replacementWaitForPID(t, ctx, f, firstPID, secondDone)
				if err := gate.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				firstOut := finishReplacementRace(t, ctx, firstDone)
				secondOut := finishReplacementRace(t, ctx, secondDone)
				replaced, mutated := firstOut, secondOut
				if !replacementFirst {
					replaced, mutated = secondOut, firstOut
				}
				var wantReplacement, wantMutation error
				if replacementFirst {
					switch operation {
					case "same-identity", "unbind":
						wantMutation = user.ErrNotFound
					case "sign-in":
						wantMutation = code.ErrInvalid
					case "reauthenticate":
						wantMutation = user.ErrNotAnchor
					case "refresh-other":
						wantMutation = user.ErrInvalidGrant
					}
				} else {
					switch operation {
					case "same-identity", "unbind":
						wantReplacement = user.ErrNotFound
					case "freeze":
						wantReplacement = user.ErrUserFrozen
					case "delete", "logout", "revoke-others", "admin-revoke-all":
						wantReplacement = user.ErrInvalidToken
					}
				}
				if !errors.Is(replaced.err, wantReplacement) || !errors.Is(mutated.err, wantMutation) {
					t.Fatalf("replacement=%v want=%v mutation=%v want=%v", replaced.err, wantReplacement, mutated.err, wantMutation)
				}
				rows, err := f.repo.Q().ListActiveIdentitiesByUser(ctx, p.UserID)
				if err != nil {
					t.Fatal(err)
				}
				wantPhone := phone2
				if wantReplacement != nil {
					wantPhone = phone1
					if operation == "same-identity" {
						wantPhone = thirdPhone
					}
					if operation == "unbind" {
						wantPhone = ""
					}
				}
				wantCount := 1
				if operation == "unbind" && replacementFirst {
					wantCount = 2
				}
				if len(rows) != wantCount {
					t.Fatalf("active identities=%d want=%d", len(rows), wantCount)
				}
				var phoneCount int
				for _, row := range rows {
					if row.Kind != enum.IdentityPhone {
						continue
					}
					phoneCount++
					digest, _ := f.dig.Digest(wantPhone)
					if row.SubjectDigest == nil || *row.SubjectDigest != digest {
						t.Fatal("wrong final phone identity")
					}
					if wantReplacement == nil && row.ID != replaced.identity.ID {
						t.Fatal("new identity result does not match committed row")
					}
				}
				if (wantPhone == "" && phoneCount != 0) || (wantPhone != "" && phoneCount != 1) {
					t.Fatal("lost or duplicated phone anchor")
				}
				u, err := f.repo.Q().GetUserByID(ctx, p.UserID)
				wantState := enum.UserActive
				if operation == "freeze" {
					wantState = enum.UserFrozen
				}
				if operation == "delete" {
					wantState = enum.UserPendingDeletion
				}
				if err != nil || u.State != wantState {
					t.Fatalf("user state=%v want=%v err=%v", u.State, wantState, err)
				}
				active, err := f.repo.Q().ListActiveSessionsByUser(ctx, p.UserID)
				if err != nil {
					t.Fatal(err)
				}
				wantSessions := 1
				switch operation {
				case "freeze", "delete", "admin-revoke-all":
					wantSessions = 0
				case "logout", "revoke-others":
					if replacementFirst {
						wantSessions = 0
					}
				case "unbind":
					if !replacementFirst {
						wantSessions = 2
					}
				}
				if len(active) != wantSessions {
					t.Fatalf("active sessions=%d want=%d", len(active), wantSessions)
				}
				if len(active) == 1 {
					wantSID := p.SessionID
					if !replacementFirst && (operation == "logout" || operation == "revoke-others") {
						wantSID = otherP.SessionID
					}
					if active[0].ID != wantSID {
						t.Fatal("wrong session survived concurrent mutation")
					}
				}
				var oldDeleted bool
				if err := f.pool.QueryRow(ctx, "SELECT delete_time IS NOT NULL FROM identity WHERE id=$1", oldID).Scan(&oldDeleted); err != nil {
					t.Fatal(err)
				}
				wantOldDeleted := replaced.err == nil || operation == "same-identity" || operation == "unbind"
				if oldDeleted != wantOldDeleted {
					t.Fatalf("old identity soft-deletion=%v want=%v", oldDeleted, wantOldDeleted)
				}
				// 第二请求已经通过预检并消费码才进入锁等待，拒绝不能恢复已消费证明。
				if err := f.deps.Codes.Verify(ctx, enum.IdentityPhone, enum.PurposeBind, phone2, newCode); !errors.Is(err, code.ErrExpired) {
					t.Fatalf("replacement proof was not consumed: %v", err)
				}
				for _, session := range []struct {
					id, access string
				}{{p.SessionID, current.AccessToken}, {otherP.SessionID, other.AccessToken}} {
					retained := false
					for _, row := range active {
						retained = retained || row.ID == session.id
					}
					_, err := f.svc.Authenticate(ctx, session.access)
					if retained && err != nil || !retained && !errors.Is(err, user.ErrInvalidToken) {
						t.Fatalf("access revocation disagrees with committed session: retained=%v err=%v", retained, err)
					}
				}
				// auth_time 只允许先行且成功的重新认证推进；换绑与刷新均不得推进。
				var authTime time.Time
				if err := f.pool.QueryRow(ctx, "SELECT auth_time FROM session WHERE id=$1", p.SessionID).Scan(&authTime); err != nil {
					t.Fatal(err)
				}
				wantAuthTime := p.AuthTime
				if operation == "reauthenticate" && !replacementFirst {
					wantAuthTime = *f.clock
				}
				if !authTime.Equal(wantAuthTime) {
					t.Fatalf("auth_time changed: %v want=%v", authTime, wantAuthTime)
				}
				if replaced.err == nil || operation == "same-identity" || operation == "freeze" || operation == "delete" || operation == "admin-revoke-all" {
					for _, raw := range []string{other.RefreshToken, otherRotated.RefreshToken, mutated.token.RefreshToken} {
						if raw == "" || operation == "refresh-current" && raw == mutated.token.RefreshToken {
							continue
						}
						if _, err := f.svc.Refresh(ctx, raw, meta1); !errors.Is(err, user.ErrInvalidGrant) && !errors.Is(err, user.ErrUserFrozen) {
							t.Fatalf("revoked session refresh revived: %v", err)
						}
					}
				}
				if len(active) == 1 && active[0].ID == p.SessionID {
					raw := current.RefreshToken
					if operation == "refresh-current" {
						raw = mutated.token.RefreshToken
					}
					if _, err := f.svc.Refresh(ctx, raw, meta1); err != nil {
						t.Fatalf("retained current refresh failed: %v", err)
					}
				}
			})
		}
	}
}
