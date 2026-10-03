package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/session/grace"
	"github.com/bbxx111/accountkit/session/revocation"
	"github.com/bbxx111/accountkit/user/db"
)

// Refresh 实现 refresh_token grant。
func (s *Service) Refresh(ctx context.Context, refreshToken string, meta Meta) (TokenResult, error) {
	hash := hashRefresh(refreshToken)
	ev := audit.Event{IP: meta.IP, RequestID: meta.RequestID}

	sess, err := s.d.Repo.Q().GetSessionByRefreshHash(ctx, hash)
	switch {
	case err == nil:
		ev.UserID, ev.SessionID, ev.DeviceID = sess.UserID, sess.ID, sess.DeviceID
		if _, err := s.ensureSessionRefreshable(ctx, sess, ev); err != nil {
			return TokenResult{}, err
		}
		res, rotated, err := s.rotate(ctx, sess, hash, ev)
		if err != nil {
			return TokenResult{}, err
		}
		if rotated {
			ev.Type, ev.Result = enum.EventTokenRefreshed, enum.ResultSuccess
			s.record(ctx, ev)
			return res, nil
		}
		// CAS 失败：并发刷新抢先轮换了，退到宽限路径领取同一份 pair
	case !errors.Is(err, pgx.ErrNoRows):
		return TokenResult{}, fmt.Errorf("user: lookup session: %w", err)
	}

	sess, err = s.d.Repo.Q().GetSessionByPreviousRefreshHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "UNKNOWN_TOKEN"
		s.record(ctx, ev)
		return TokenResult{}, ErrInvalidGrant
	}
	if err != nil {
		return TokenResult{}, fmt.Errorf("user: lookup previous hash: %w", err)
	}
	ev.UserID, ev.SessionID, ev.DeviceID = sess.UserID, sess.ID, sess.DeviceID
	now, err := s.ensureSessionRefreshable(ctx, sess, ev)
	if err != nil {
		return TokenResult{}, err
	}
	if sess.RotateTime == nil || now.Sub(*sess.RotateTime) > s.d.RefreshGrace {
		// 宽限外重放 = 凭证泄露信号：吊销会话，让攻击者与原持有者都必须重新登录。
		// 若吊销本身失败（DB 错误），不能把它审计成"重放已被遏制"——那会掩盖会话可能仍然
		// 存活的事实；把 DB 错误原样向上传播，调用方据此看到的是基础设施故障而非 invalid_grant。
		if err := s.revokeSession(ctx, sess.ID, enum.RevokeReuseDetected, now); err != nil {
			return TokenResult{}, err
		}
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshReuseDetected, enum.ResultFailure, "TOKEN_REUSE"
		s.record(ctx, ev)
		return TokenResult{}, ErrInvalidGrant
	}
	pair, ok, err := s.d.Grace.Get(ctx, hash)
	if err != nil {
		s.d.Logger.Warn("user: grace cache read failed", "err", err)
	}
	// 资格已在读取会话及账号后确定；缓存等待只复核期限，不重新判断宽限重放。
	now = sessionTime(s.now())
	if err := s.ensureRefreshSessionActive(ctx, sess, now, ev); err != nil {
		return TokenResult{}, err
	}
	if err != nil || !ok || !pair.AccessExpiresAt.After(now) || !pair.RefreshExpiresAt.After(now) {
		// Redis 不可用或缓存已失效：退化为拒绝但不吊销（避免把网络抖动误判为凭证泄露）。
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "GRACE_UNAVAILABLE"
		s.record(ctx, ev)
		return TokenResult{}, ErrInvalidGrant
	}
	ev.Type, ev.Result, ev.Reason = enum.EventTokenRefreshed, enum.ResultSuccess, "GRACE_REPLAY"
	s.record(ctx, ev)
	return TokenResult{
		AccessToken: pair.AccessToken, RefreshToken: pair.RefreshToken,
		ExpiresIn: int(pair.AccessExpiresAt.Sub(now).Seconds()), RefreshExpiresIn: int(pair.RefreshExpiresAt.Sub(now).Seconds()),
		Scope: pair.Scope, UserID: sess.UserID,
	}, nil
}

// ensureRefreshSessionActive 仅分类会话自身的吊销/期限，不产生撤销副作用。
func (s *Service) ensureRefreshSessionActive(ctx context.Context, sess db.Session, now time.Time, ev audit.Event) error {
	if sess.RevokeTime != nil {
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "SESSION_REVOKED"
		s.record(ctx, ev)
		return ErrInvalidGrant
	}
	if !sessionActiveAt(sess, now) {
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "SESSION_EXPIRED"
		s.record(ctx, ev)
		return ErrInvalidGrant
	}
	return nil
}

// ensureSessionRefreshable 在必要读取后返回最新资格判定时刻；只有账号状态拒绝会吊销。
func (s *Service) ensureSessionRefreshable(ctx context.Context, sess db.Session, ev audit.Event) (time.Time, error) {
	now := sessionTime(s.now())
	if err := s.ensureRefreshSessionActive(ctx, sess, now, ev); err != nil {
		return now, err
	}
	u, err := s.d.Repo.Q().GetUserByID(ctx, sess.UserID)
	if err != nil {
		return now, fmt.Errorf("user: load user for refresh: %w", err)
	}
	now = sessionTime(s.now())
	if err := s.ensureRefreshSessionActive(ctx, sess, now, ev); err != nil {
		return now, err
	}
	switch u.State {
	case enum.UserActive:
		return now, nil
	case enum.UserFrozen:
		// 吊销失败同样必须向上传播（而不是继续返回 ErrUserFrozen）：调用方不能把一次
		// "遏制动作本身失败"的情形误当作"账号被冻结、已正常拒绝"处理。
		if err := s.revokeSession(ctx, sess.ID, enum.RevokeUserFrozen, now); err != nil {
			return now, err
		}
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "USER_FROZEN"
		s.record(ctx, ev)
		return now, ErrUserFrozen
	default: // PENDING_DELETION / DELETED：会话不得续期
		if err := s.revokeSession(ctx, sess.ID, enum.RevokeUserDeleted, now); err != nil {
			return now, err
		}
		ev.Type, ev.Result, ev.Reason = enum.EventRefreshRejected, enum.ResultFailure, "USER_"+u.State.String()
		s.record(ctx, ev)
		return now, ErrInvalidGrant
	}
}

// revokeSession 吊销单个会话并写吊销集。吊销集写入无论 DB 更新是否成功都会尝试
// （拉黑 access token 代价低且安全）；但 DB 更新失败时把错误包装后返回，调用方必须
// 向上传播——不能把一次失败的遏制动作当作已完成的业务拒绝来审计或返回哨兵错误。
func (s *Service) revokeSession(ctx context.Context, sid string, reason enum.RevokeReason, now time.Time) error {
	_, dbErr := s.d.Repo.Q().RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: &reason, Now: now})
	s.revokeInSet(ctx, sid)
	if dbErr != nil {
		s.d.Logger.Error("user: revoke session failed", "sid", sid, "reason", reason.String(), "err", dbErr)
		return fmt.Errorf("user: revoke session: %w", dbErr)
	}
	return nil
}

// rotate 在事务内对会话行加锁后 CAS 轮换 refresh，并在提交前把新 pair 写入宽限缓存。
// 并发刷新者在行锁上排队：胜者提交时缓存已就位，失败者随后走宽限路径必然命中同一 pair。
// 返回 rotated=false 表示加锁后发现哈希已被他人轮换（CAS 未命中）。
func (s *Service) rotate(ctx context.Context, sess db.Session, oldHash []byte, ev audit.Event) (TokenResult, bool, error) {
	var res TokenResult
	rotated := false
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockSessionByID(ctx, sess.ID)
		if err != nil {
			return fmt.Errorf("user: lock session: %w", err)
		}
		if !bytes.Equal(locked.RefreshTokenHash, oldHash) || locked.RevokeTime != nil {
			return nil // 他人已轮换或已吊销：交给调用方的宽限/拒绝路径
		}
		u, err := q.GetUserByID(ctx, locked.UserID)
		if err != nil {
			return fmt.Errorf("user: load user: %w", err)
		}
		if u.State != enum.UserActive {
			// 账号在拿到会话行锁之前（例如并发冻结）已经不再 Active：不要再签出一份新
			// pair。交给调用方的宽限/拒绝路径重新读取账号状态并按 frozen/deleted 正确处理。
			return nil
		}
		hasAnchor, err := s.userHasAnchor(ctx, q, locked.UserID)
		if err != nil {
			return err
		}
		now := sessionTime(s.now())
		if err := s.ensureRefreshSessionActive(ctx, locked, now, ev); err != nil {
			return err
		}
		scope := deriveScope(u.State, hasAnchor)

		plain, newHash, err := newRefreshToken()
		if err != nil {
			return err
		}
		expire := now.Add(s.d.RefreshTTL)
		n, err := q.RotateSession(ctx, db.RotateSessionParams{ID: locked.ID, OldHash: oldHash, NewHash: newHash, Now: now, RefreshExpireTime: expire})
		if err != nil {
			return fmt.Errorf("user: rotate: %w", err)
		}
		if n == 0 {
			return nil
		}
		locked.RefreshExpireTime = expire
		res, err = s.tokensFor(locked, plain, scope, now)
		if err != nil {
			return err
		}
		if err := s.d.Grace.Put(ctx, oldHash, grace.Pair{
			AccessToken: res.AccessToken, RefreshToken: plain, Scope: scope,
			AccessExpiresAt: now.Add(s.d.AccessTTL), RefreshExpiresAt: expire,
		}, s.d.RefreshGrace); err != nil {
			// Redis 不可用：轮换仍然成功，只是宽限内重放会被拒绝（退化语义，见 Refresh 的宽限分支）。
			s.d.Logger.Warn("user: grace cache write failed; in-grace replays will be rejected", "sid", locked.ID, "err", err)
		}
		rotated = true
		return nil
	})
	if err != nil {
		return TokenResult{}, false, err
	}
	return res, rotated, nil
}

// Revoke 实现 RFC 7009：吊销 refresh 对应的会话；未知 token 静默成功。
func (s *Service) Revoke(ctx context.Context, refreshToken string, meta Meta) error {
	now := s.now()
	hash := hashRefresh(refreshToken)
	sess, err := s.d.Repo.Q().GetSessionByRefreshHash(ctx, hash)
	if errors.Is(err, pgx.ErrNoRows) {
		sess, err = s.d.Repo.Q().GetSessionByPreviousRefreshHash(ctx, hash)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("user: lookup session for revoke: %w", err)
	}
	if sess.RevokeTime != nil {
		return nil
	}
	reason := enum.RevokeUserLogout
	if _, err := s.d.Repo.Q().RevokeSession(ctx, db.RevokeSessionParams{ID: sess.ID, Reason: &reason, Now: now}); err != nil {
		return fmt.Errorf("user: revoke: %w", err)
	}
	s.revokeInSet(ctx, sess.ID)
	s.record(ctx, audit.Event{Type: enum.EventSessionRevoked, Result: enum.ResultSuccess, Reason: reason.String(),
		UserID: sess.UserID, SessionID: sess.ID, DeviceID: sess.DeviceID, IP: meta.IP, RequestID: meta.RequestID})
	return nil
}

// Authenticate 校验 access token 并返回 Principal。吊销集不可用时 fail-open。
func (s *Service) Authenticate(ctx context.Context, rawAccess string) (Principal, error) {
	c, err := s.d.Signer.Parse(rawAccess)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !ids.Valid(ids.User, c.UserID) || !ids.Valid(ids.Session, c.SessionID) {
		return Principal{}, fmt.Errorf("%w: malformed sub/sid", ErrInvalidToken)
	}
	revoked, err := s.d.Revocation.IsRevoked(ctx, c.SessionID)
	if err != nil {
		if errors.Is(err, revocation.ErrUnavailable) {
			s.d.Logger.Warn("user: revocation check unavailable (fail-open)", "sid", c.SessionID)
		} else {
			s.d.Logger.Warn("user: revocation check failed (fail-open)", "sid", c.SessionID, "err", err)
		}
	} else if revoked {
		return Principal{}, fmt.Errorf("%w: session revoked", ErrInvalidToken)
	}
	return Principal{UserID: c.UserID, SessionID: c.SessionID, Scope: c.Scope, AuthTime: c.AuthTime}, nil
}

// SessionInfo 是会话列表项。
type SessionInfo struct {
	ID           string
	DeviceID     string
	DeviceName   string
	CreateTime   time.Time
	LastUsedTime time.Time
	IsCurrent    bool
}

// ListSessions 列出用户的活跃会话（不分页）。
func (s *Service) ListSessions(ctx context.Context, userID, currentSID string) ([]SessionInfo, error) {
	rows, err := s.d.Repo.Q().ListActiveSessionsByUser(ctx, db.ListActiveSessionsByUserParams{UserID: userID, Now: sessionTime(s.now())})
	if err != nil {
		return nil, fmt.Errorf("user: list sessions: %w", err)
	}
	out := make([]SessionInfo, 0, len(rows))
	for _, r := range rows {
		info := SessionInfo{ID: r.ID, DeviceID: r.DeviceID, CreateTime: r.CreateTime, LastUsedTime: r.LastUsedTime, IsCurrent: r.ID == currentSID}
		if r.DeviceName != nil {
			info.DeviceName = *r.DeviceName
		}
		out = append(out, info)
	}
	return out, nil
}

// RevokeSession 踢出指定设备；不属于该用户或已吊销 → ErrNotFound（AGENTS.md：外部/不存在资源一律 404）。
func (s *Service) RevokeSession(ctx context.Context, userID, sid string, meta Meta) error {
	now := s.now()
	if _, err := s.d.Repo.Q().GetActiveSessionByIDAndUser(ctx, db.GetActiveSessionByIDAndUserParams{ID: sid, UserID: userID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("user: lookup session: %w", err)
	}
	reason := enum.RevokeUserRevokedDevice
	n, err := s.d.Repo.Q().RevokeSession(ctx, db.RevokeSessionParams{ID: sid, Reason: &reason, Now: now})
	if err != nil {
		return fmt.Errorf("user: revoke session: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	s.revokeInSet(ctx, sid)
	s.record(ctx, audit.Event{Type: enum.EventSessionRevoked, Result: enum.ResultSuccess, Reason: reason.String(), UserID: userID, SessionID: sid, IP: meta.IP, RequestID: meta.RequestID})
	return nil
}

// RevokeOtherSessions 吊销除当前会话外的全部活跃会话。
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, currentSID string, meta Meta) error {
	now := s.now()
	var revoked []string
	err := s.d.Repo.WithTx(ctx, func(q *db.Queries) error {
		// 多行会话写入遵循 user → session 锁序；不存在的用户仍是无操作成功。
		if _, err := q.LockUserByID(ctx, userID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("user: lock user for session revocation: %w", err)
		}
		var err error
		revoked, err = s.revokeAllForUser(ctx, q, userID, &currentSID, enum.RevokeUserRevokedDevice, now)
		return err
	})
	if err != nil {
		return err
	}
	s.afterRevokeAll(ctx, revoked, enum.RevokeUserRevokedDevice, audit.Event{Actor: enum.ActorUser, UserID: userID, IP: meta.IP, RequestID: meta.RequestID})
	return nil
}

// revokeAllForUser 在给定事务内吊销用户全部活跃会话（可排除一个），返回被吊销的 sid。
// 调用方负责在事务提交后写吊销集与审计。
func (s *Service) revokeAllForUser(ctx context.Context, q *db.Queries, userID string, except *string, reason enum.RevokeReason, now time.Time) ([]string, error) {
	sids, err := q.RevokeSessionsByUser(ctx, db.RevokeSessionsByUserParams{UserID: userID, ExceptID: except, Reason: &reason, Now: now})
	if err != nil {
		return nil, fmt.Errorf("user: revoke sessions: %w", err)
	}
	return sids, nil
}

// SessionRetention 是已吊销/已过期会话行的保留期（§3.2：30 天后清理）。不进配置：它只影响
// 排障时还能看到多久以前的会话记录，与安全无关（吊销与过期本身即时生效）。
const SessionRetention = 30 * 24 * time.Hour

const sessionCleanupBatch = 1000

// CleanupSessions 物理删除吊销时间早于 now − SessionRetention、或从未吊销但 refresh 过期时间早于该点的会话行，
// 每批 sessionCleanupBatch 行，删到一批不满为止；返回删除总数。维护任务 cleanup_sessions 每轮调用一次。
func (s *Service) CleanupSessions(ctx context.Context) (int64, error) {
	before := s.now().Add(-SessionRetention)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.d.Repo.Q().DeleteStaleSessions(ctx, db.DeleteStaleSessionsParams{Before: before, BatchSize: sessionCleanupBatch})
		if err != nil {
			return total, fmt.Errorf("user: delete stale sessions: %w", err)
		}
		total += n
		if n < sessionCleanupBatch {
			return total, nil
		}
	}
}
