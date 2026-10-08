package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

// ImportAnchor 是历史账号的 PHONE/EMAIL 登录锚点。Target 仅作为输入，
// 库使用当前归一化、加密和摘要规则存储；创建、更新时间必须提供且按原值保留。
type ImportAnchor struct {
	Kind       enum.IdentityKind
	Target     string
	CreateTime time.Time
	UpdateTime time.Time
}

// ImportAccount 是受控离线工具提供的历史账号。ID 必须为合法 u_ ID，
// 创建、更新时间必须提供；ACTIVE 至少有一个锚点且无删除时间，
// DELETED 必须是无显示名、无锚点的匿名墓碑，其历史删除和匿名化时间可保留。
type ImportAccount struct {
	ID          string
	State       enum.UserState
	DisplayName string
	CreateTime  time.Time
	UpdateTime  time.Time
	DeleteTime  *time.Time
	PurgeTime   *time.Time
	Anchors     []ImportAnchor
}

// ImportAccounts 在调用者提供的同库事务中导入历史账号，不另开连接、
// 不提交或回滚事务，不发码、建会话、签发令牌或访问外部身份提供方。
// 调用者必须停写并核验目标 schema、管理批次及重复执行记录；任何错误后
// 必须回滚整个事务，成功时由调用者连同业务引用一起提交。它不自动合并账号。
// 错误消息不包含输入身份或数据库详情；errors.Is/errors.As 仍可检查原始原因，
// 调用者不得未经脱敏将解包后的数据库错误记录或输出。
func (s *Service) ImportAccounts(ctx context.Context, tx pgx.Tx, accounts []ImportAccount) error {
	if tx == nil {
		return fmt.Errorf("%w: import transaction is required", ErrInvalidArgument)
	}
	q := db.New(tx)
	for index, account := range accounts {
		if err := validateImportAccount(account); err != nil {
			return fmt.Errorf("user: import account %d: %w", index, err)
		}
		type normalizedAnchor struct{ target, prefix, suffix string }
		normalizedAnchors := make([]normalizedAnchor, len(account.Anchors))
		for anchorIndex, anchor := range account.Anchors {
			if anchor.Kind != enum.IdentityPhone && anchor.Kind != enum.IdentityEmail {
				return fmt.Errorf("%w: import account %d anchor %d must be PHONE or EMAIL", ErrInvalidArgument, index, anchorIndex)
			}
			if anchor.CreateTime.IsZero() || anchor.UpdateTime.IsZero() {
				return fmt.Errorf("%w: import account %d anchor %d times are required", ErrInvalidArgument, index, anchorIndex)
			}
			normalized, prefix, suffix, err := s.normalizeTarget(anchor.Kind, anchor.Target)
			if err != nil {
				// 底层解析错误可能携带输入，不向导入错误透传。
				return fmt.Errorf("%w: import account %d anchor %d", ErrInvalidTarget, index, anchorIndex)
			}
			normalizedAnchors[anchorIndex] = normalizedAnchor{normalized, prefix, suffix}
		}
		name := account.DisplayName
		var displayName *string
		if name != "" {
			displayName = &name
		}
		if err := q.ImportUser(ctx, db.ImportUserParams{
			ID: account.ID, State: account.State, DisplayName: displayName,
			CreateTime: account.CreateTime, UpdateTime: account.UpdateTime,
			DeleteTime: account.DeleteTime, PurgeTime: account.PurgeTime,
		}); err != nil {
			return importDatabaseError(index, "account insert", err)
		}
		for anchorIndex, anchor := range account.Anchors {
			n := normalizedAnchors[anchorIndex]
			normalized, prefix, suffix := n.target, n.prefix, n.suffix
			_, err := q.FindActiveIdentityByDigests(ctx, db.FindActiveIdentityByDigestsParams{
				Kind: anchor.Kind, Digests: s.d.Digester.AllDigests(normalized),
			})
			if err == nil {
				return fmt.Errorf("%w: import account %d anchor %d", ErrIdentityConflict, index, anchorIndex)
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return importDatabaseError(index, "identity lookup", err)
			}
			digest, digestVersion := s.d.Digester.Digest(normalized)
			ciphertext, cipherVersion, err := s.d.Cipher.Encrypt(normalized)
			if err != nil {
				return &importFailure{operation: "identity encryption", index: index, cause: err}
			}
			id, err := ids.New(ids.Identity)
			if err != nil {
				return &importFailure{operation: "identity ID generation", index: index, cause: err}
			}
			dv, cv := int16(digestVersion), int16(cipherVersion)
			if err := q.ImportIdentity(ctx, db.ImportIdentityParams{
				ID: id, UserID: account.ID, Kind: anchor.Kind, SubjectDigest: &digest,
				DigestKeyVersion: &dv, SubjectCiphertext: ciphertext, CipherKeyVersion: &cv,
				HintPrefix: &prefix, HintSuffix: &suffix, CreateTime: anchor.CreateTime, UpdateTime: anchor.UpdateTime,
			}); err != nil {
				return importDatabaseError(index, "identity insert", err)
			}
		}
	}
	return nil
}

func validateImportAccount(account ImportAccount) error {
	if !ids.Valid(ids.User, account.ID) {
		return fmt.Errorf("%w: invalid import account ID", ErrInvalidArgument)
	}
	switch account.State {
	case enum.UserActive:
		if len(account.Anchors) == 0 || account.DeleteTime != nil || account.PurgeTime != nil {
			return fmt.Errorf("%w: ACTIVE import requires anchors and no deletion times", ErrInvalidArgument)
		}
	case enum.UserDeleted:
		if account.DisplayName != "" || len(account.Anchors) != 0 {
			return fmt.Errorf("%w: DELETED import requires an anonymous tombstone", ErrInvalidArgument)
		}
	default:
		return ErrInvalidState
	}
	if account.CreateTime.IsZero() || account.UpdateTime.IsZero() {
		return fmt.Errorf("%w: import account times are required", ErrInvalidArgument)
	}
	return nil
}

// importFailure 保留可诊断原因，但 Error 不渲染 PostgreSQL Detail（可能包含摘要或 PII）。
type importFailure struct {
	operation string
	index     int
	cause     error
}

func (e *importFailure) Error() string {
	return fmt.Sprintf("user: import account %d: %s failed", e.index, e.operation)
}
func (e *importFailure) Unwrap() error { return e.cause }
func importDatabaseError(index int, operation string, err error) error {
	if IsUniqueViolation(err) {
		err = errors.Join(ErrIdentityConflict, err)
	}
	return &importFailure{operation: operation, index: index, cause: err}
}
