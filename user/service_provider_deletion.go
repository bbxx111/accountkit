package user

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// ProviderDeletionStatus 是第三方平台数据删除请求的处理结果，不落库。
type ProviderDeletionStatus string

const (
	// ProviderDeletionDeleted：该第三方身份已被抹除，或本就不存在、与本请求无关。
	ProviderDeletionDeleted ProviderDeletionStatus = "DELETED"
	// ProviderDeletionScheduled：账号已处于注销冷静期，到 PurgeTime 匿名化。
	ProviderDeletionScheduled ProviderDeletionStatus = "SCHEDULED"
	// ProviderDeletionBlocked：未改动数据，宿主注销检查拒绝或账号冻结，需宿主跟进。
	ProviderDeletionBlocked ProviderDeletionStatus = "BLOCKED"
)

// ProviderDeletionResult 是同步返回给宿主的处理结果。ConfirmationCode 等于数据删除审计事件 ID，
// 每次调用唯一且不含第三方用户 id；审计为尽力写入，需要可靠状态页的宿主应自行保存本结果。
type ProviderDeletionResult struct {
	ConfirmationCode string
	Status           ProviderDeletionStatus
	// PurgeTime 仅在 SCHEDULED 时非零。
	PurgeTime time.Time
}

var errProviderDeletionBlocked = errors.New("user: provider deletion blocked")

// providerSubjectHint 返回第三方 subject 的脱敏提示：SHA-256 十六进制的前 8 字符，不暴露原值前缀。
func providerSubjectHint(subject string) string {
	sum := sha256.Sum256([]byte(subject))
	return audit.Hint(hex.EncodeToString(sum[:]))
}

// DeleteProviderIdentityData 处理第三方平台按 subject 发起的数据删除（provider-data-deletion 规格）。
// 去掉该身份后仍有有效 Enabled 身份时抹除该身份并吊销以它为来源的会话，不检查 Required；
// 否则 ACTIVE 账号转入注销，宿主阻止或账号冻结返回 BLOCKED 且不改动数据；已在冷静期返回 SCHEDULED。
// requestTime 之前尚未创建的身份与本请求无关。判定与写入在同一事务、账号行锁下完成。
// BeforeDelete 返回 ErrDeletionBlocked 以外的错误时返回错误，宿主应让平台重试。
func (s *Service) DeleteProviderIdentityData(ctx context.Context, kind enum.IdentityKind, subject string, requestTime time.Time, meta Meta) (ProviderDeletionResult, error) {
	if !kind.Valid() || kind.IsAnchor() || subject == "" {
		return ProviderDeletionResult{}, fmt.Errorf("%w: provider kind and subject are required", ErrInvalidArgument)
	}
	if requestTime.IsZero() {
		// 零时间会让所有身份都显得“请求之后才创建”，请求被静默忽略。
		return ProviderDeletionResult{}, fmt.Errorf("%w: request time is required", ErrInvalidArgument)
	}
	code, err := uuid.NewV7()
	if err != nil {
		return ProviderDeletionResult{}, fmt.Errorf("user: confirmation code: %w", err)
	}
	res := ProviderDeletionResult{ConfirmationCode: code.String(), Status: ProviderDeletionDeleted}
	var (
		target           db.Identity
		unbound, deleted bool
		revoked          []string
	)
	err = s.d.Repo.withDomainTxRaw(ctx, func(tx pgx.Tx, q *db.Queries) error {
		found, err := q.FindActiveIdentityByProviderSubject(ctx, db.FindActiveIdentityByProviderSubjectParams{Kind: kind, ProviderSubject: &subject})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("user: find provider identity: %w", err)
		}
		u, err := lockUserAs(ctx, q, found.UserID, ErrNotFound)
		if err != nil {
			return err
		}
		ident, err := q.GetActiveIdentityByIDAndUser(ctx, db.GetActiveIdentityByIDAndUserParams{ID: found.ID, UserID: u.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // 加锁前已被并发解绑或匿名化
		}
		if err != nil {
			return fmt.Errorf("user: relock provider identity: %w", err)
		}
		target = ident
		if ident.CreateTime.After(requestTime) {
			return nil // 请求签发后重新绑定的身份与本请求无关
		}
		if u.State == enum.UserPendingDeletion {
			res.Status = ProviderDeletionScheduled
			if u.PurgeTime != nil {
				res.PurgeTime = *u.PurgeTime
			}
			return nil
		}
		idents, err := q.ListActiveIdentitiesByUser(ctx, u.ID)
		if err != nil {
			return fmt.Errorf("user: list identities: %w", err)
		}
		policy := s.IdentityPolicy()
		remaining := false
		for _, other := range idents {
			if other.ID != ident.ID && policy.Allows(other.Kind) {
				remaining = true
				break
			}
		}
		if remaining {
			// 与 UnbindIdentity 相同：任一未吊销会话缺少认证来源时整事务拒绝，不按未命中放行。
			missingSources, err := q.HasSessionsWithoutAuthenticationIdentities(ctx, u.ID)
			if err != nil {
				return fmt.Errorf("user: check authentication source integrity: %w", err)
			}
			if missingSources {
				return fmt.Errorf("user: invariant violated: unrevoked session has empty authentication identities")
			}
			now := sessionTime(s.now())
			repl, err := randomHex(32)
			if err != nil {
				return err
			}
			n, err := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: ident.ID, UserID: u.ID, Replacement: repl, Now: now})
			if err != nil {
				return fmt.Errorf("user: anonymize provider identity: %w", err)
			}
			if n != 1 {
				return fmt.Errorf("user: anonymize provider identity: %d rows affected", n)
			}
			reason := enum.RevokeIdentityUnbound
			revoked, err = q.RevokeSessionsByIdentity(ctx, db.RevokeSessionsByIdentityParams{UserID: u.ID, IdentityID: ident.ID, Now: now, Reason: &reason})
			if err != nil {
				return fmt.Errorf("user: revoke identity sessions: %w", err)
			}
			unbound = true
			return nil
		}
		if u.State != enum.UserActive {
			return errProviderDeletionBlocked
		}
		row, sids, err := s.softDeleteLocked(ctx, tx, q, u, s.now())
		if errors.Is(err, ErrDeletionBlocked) {
			return errProviderDeletionBlocked
		}
		if err != nil {
			return err
		}
		revoked, deleted = sids, true
		res.Status = ProviderDeletionScheduled
		if row.PurgeTime != nil {
			res.PurgeTime = *row.PurgeTime
		}
		return nil
	})
	if errors.Is(err, errProviderDeletionBlocked) {
		res, err = ProviderDeletionResult{ConfirmationCode: res.ConfirmationCode, Status: ProviderDeletionBlocked}, nil
		unbound, deleted, revoked = false, false, nil
	}
	if err == nil {
		// 主事务已提交（含 BLOCKED）；历史解除行失败仍返回错误，重试时主流程幂等。
		err = s.scrubUnboundProviderIdentities(ctx, kind, subject, requestTime)
	}
	hint := providerSubjectHint(subject)
	tmpl := audit.AuthenticationEvent{Actor: enum.ActorSystem, UserID: target.UserID, IP: meta.IP, RequestID: meta.RequestID}
	ev := tmpl
	ev.ID, ev.Type, ev.IdentityKind, ev.SubjectHint = code, enum.EventProviderDataDeletionProcessed, kind, hint
	if err != nil {
		ev.Result = enum.ResultFailure
		s.record(ctx, ev)
		return ProviderDeletionResult{}, err
	}
	switch {
	case unbound:
		s.afterRevokeAll(ctx, revoked, enum.RevokeIdentityUnbound, tmpl)
		unbind := tmpl
		unbind.Type, unbind.Result, unbind.IdentityID, unbind.IdentityKind, unbind.SubjectHint = enum.EventIdentityUnbound, enum.ResultSuccess, target.ID, target.Kind, hint
		s.record(ctx, unbind)
	case deleted:
		s.afterSoftDelete(ctx, revoked, tmpl)
	}
	ev.Result, ev.Reason = enum.ResultSuccess, string(res.Status)
	s.record(ctx, ev)
	return res, nil
}

// scrubUnboundProviderIdentities 抹除 requestTime 之前创建、已解除但仍保存原 subject 的同类型身份行。
// 每行独立事务：先锁账号行再改身份行（账号行 → 身份行锁序）；账号不存在则跳过；匿名化幂等。
func (s *Service) scrubUnboundProviderIdentities(ctx context.Context, kind enum.IdentityKind, subject string, requestTime time.Time) error {
	var rows []db.Identity
	err := s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		var err error
		rows, err = q.ListDeletedIdentitiesByProviderSubject(ctx, db.ListDeletedIdentitiesByProviderSubjectParams{Kind: kind, ProviderSubject: &subject})
		return err
	})
	if err != nil {
		return fmt.Errorf("user: list unbound provider identities: %w", err)
	}
	for _, row := range rows {
		if row.CreateTime.After(requestTime) {
			continue // 请求签发后才创建的身份与本请求无关
		}
		err := s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
			if _, err := lockUserAs(ctx, q, row.UserID, ErrNotFound); errors.Is(err, ErrNotFound) {
				return nil
			} else if err != nil {
				return err
			}
			repl, err := randomHex(32)
			if err != nil {
				return err
			}
			if _, err := q.AnonymizeIdentity(ctx, db.AnonymizeIdentityParams{ID: row.ID, UserID: row.UserID, Replacement: repl, Now: sessionTime(s.now())}); err != nil {
				return fmt.Errorf("user: anonymize unbound provider identity: %w", err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
