package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// accountMayAct 是"已登录用户能否在账号上产生新的写入"（绑定、解绑等）的统一判定：
// ACTIVE → nil；FROZEN → ErrUserFrozen；PENDING_DELETION/DELETED → ErrInvalidToken——这类会话按
// establishSession / DeleteMe / purge 的不变式本应已被吊销，Redis fail-open 窗口下仍可能放行到这里，
// 让客户端把它当会话失效丢弃凭证，而不是允许在一个即将/已被清除的账号下产生新的写入。
func accountMayAct(u db.UserAccount) error {
	switch u.State {
	case enum.UserActive:
		return nil
	case enum.UserFrozen:
		return ErrUserFrozen
	default: // PENDING_DELETION / DELETED
		return fmt.Errorf("%w: account state %s", ErrInvalidToken, u.State)
	}
}

// requireState 是状态迁移（§1.1 迁移表）的前置断言：账号须处于 want。
// FROZEN 一律 ErrUserFrozen（与登录/刷新一致，403；"冻结账号要注销需先解冻"）；
// 其余不匹配 → ErrInvalidState（400 FAILED_PRECONDITION）。
func requireState(u db.UserAccount, want enum.UserState) error {
	switch {
	case u.State == want:
		return nil
	case u.State == enum.UserFrozen:
		return ErrUserFrozen
	default:
		return fmt.Errorf("%w: state is %s", ErrInvalidState, u.State)
	}
}

// lockUserAs 对用户行加锁；行不存在时返回 missing。C 端传 ErrInvalidToken（持合法 token 却找不到用户），
// 管理面传 ErrNotFound（路径里的 user 不存在 → 404）。
func lockUserAs(ctx context.Context, q *db.Queries, userID string, missing error) (db.UserAccount, error) {
	u, err := q.LockUserByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.UserAccount{}, fmt.Errorf("%w: user not found", missing)
	}
	if err != nil {
		return db.UserAccount{}, fmt.Errorf("user: lock user: %w", err)
	}
	return u, nil
}

// lockUser 是 C 端写路径的第一步（锁序：用户行 → 身份/会话行）；行不存在 → ErrInvalidToken。
func lockUser(ctx context.Context, q *db.Queries, userID string) (db.UserAccount, error) {
	return lockUserAs(ctx, q, userID, ErrInvalidToken)
}
