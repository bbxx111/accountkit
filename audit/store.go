package audit

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/ids"
)

// RetentionBatchSize 是保留期清理每批删除的行数（限批避免单事务锁太多行）。
const RetentionBatchSize = 1000

// maxIDGenAttempts 是单行 id 在批内撞见重复时的重新生成上限。
const maxIDGenAttempts = 8

// newID 是 id 生成的测试替换点：生产代码始终等于 ids.New；测试用 SetNewIDForTest
// （见 export_test.go）临时替换，制造确定性的跨批主键冲突而不必依赖真实的同毫秒撞车概率。
var newID = ids.New

// Store 是 audit_event 表的写入与清理后端。读侧（管理端分页）在阶段 6。
type Store struct {
	q *db.Queries
}

// NewStore 构造 Store；不做 I/O。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{q: db.New(pool)} }

// InsertBatch 用 pgx 批处理写入一批事件（每行一条 INSERT）。pgx 的 SendBatch 在隐式事务内执行：
// 任一行失败（或连接不可用）整批回滚，因此返回的 failed 要么 0 要么 len(events)，err 为首个错误。
// 事件 id 在此生成；空串写 NULL；ip 解析失败写 NULL 而不是让整批失败。
//
// ids.New 每毫秒只有 22 bit 随机量：一批上千条事件的生成集中在同一毫秒时，生日碰撞概率不可忽略
// （约 1000 个 id/ms 时约 10%），因此这里做两层防护：
//  1. 批内去重——同一次调用生成的 id 不允许重复，撞了就在本批内重新生成（每行至多
//     maxIDGenAttempts 次，用尽仍重复视为不可恢复错误）；
//  2. 批内去重防不住跨批碰撞（两次 InsertBatch 调用之间撞见同一 id），这类冲突只能在写库时
//     发现：命中 audit_event_pkey 唯一约束时，整批 id 重新生成后重试一次；仍失败则按今天的语义
//     返回 failed = len(events) 与该错误。
func (s *Store) InsertBatch(ctx context.Context, events []Event) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	params, err := s.paramsWithUniqueIDs(events)
	if err != nil {
		return len(events), err
	}
	failed, err := s.insert(ctx, params)
	if err != nil && isAuditEventPKConflict(err) {
		retryParams, rerr := s.paramsWithUniqueIDs(events)
		if rerr != nil {
			return len(events), rerr
		}
		failed, err = s.insert(ctx, retryParams)
	}
	return failed, err
}

// paramsWithUniqueIDs 为每个事件生成一个本次调用内唯一的 id 并映射为插入参数。
func (s *Store) paramsWithUniqueIDs(events []Event) ([]db.InsertAuditEventsParams, error) {
	params := make([]db.InsertAuditEventsParams, 0, len(events))
	seen := make(map[string]struct{}, len(events))
	for _, e := range events {
		id, err := uniqueID(seen)
		if err != nil {
			return nil, err
		}
		params = append(params, toParams(id, e))
	}
	return params, nil
}

// uniqueID 生成一个不在 seen 中的 id 并登记进 seen；连续 maxIDGenAttempts 次撞见批内重复视为
// 不可恢复错误（正常情况下 22 bit 随机量连续多次撞见批内重复的概率极低）。
func uniqueID(seen map[string]struct{}) (string, error) {
	for attempt := 0; attempt < maxIDGenAttempts; attempt++ {
		id, err := newID(ids.AuditEvent)
		if err != nil {
			return "", fmt.Errorf("audit: generate id: %w", err)
		}
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			return id, nil
		}
	}
	return "", fmt.Errorf("audit: could not generate a unique id within batch after %d attempts", maxIDGenAttempts)
}

// isAuditEventPKConflict 报告 err 是否为 audit_event 主键唯一冲突（SQLSTATE 23505），
// 即两次 InsertBatch 调用之间撞见了同一个 id。约束名 "audit_event_pkey" 由迁移显式命名
// （0001_init.up.sql 的 CONSTRAINT audit_event_pkey PRIMARY KEY），不依赖 PostgreSQL 的自动命名规则。
func isAuditEventPKConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "audit_event_pkey"
}

func (s *Store) insert(ctx context.Context, params []db.InsertAuditEventsParams) (int, error) {
	br := s.q.InsertAuditEvents(ctx, params)
	var first error
	br.Exec(func(_ int, err error) {
		if err != nil && first == nil {
			first = err
		}
	})
	if err := br.Close(); err != nil && first == nil {
		first = err
	}
	if first != nil {
		return len(params), fmt.Errorf("audit: insert batch of %d events failed: %w", len(params), first)
	}
	return 0, nil
}

// toParams 把 Event 映射为插入参数：空串 → NULL，零值 identity_kind → NULL，ip 解析失败 → NULL。
func toParams(id string, e Event) db.InsertAuditEventsParams {
	p := db.InsertAuditEventsParams{
		ID: id, EventType: e.Type, ActorKind: e.Actor, Result: e.Result, OccurTime: e.OccurTime,
		UserID: nullStr(e.UserID), SessionID: nullStr(e.SessionID), SubjectHint: nullStr(e.SubjectHint), Reason: nullStr(e.Reason),
		DeviceID: nullStr(e.DeviceID), RequestID: nullStr(e.RequestID),
		AdminIssuer: nullStr(e.AdminIssuer), AdminSubject: nullStr(e.AdminSubject), AdminUsername: nullStr(e.AdminUsername),
	}
	if e.IdentityKind != enum.IdentityKindUnspecified {
		k := e.IdentityKind
		p.IdentityKind = &k
	}
	if addr, err := netip.ParseAddr(e.IP); err == nil {
		p.Ip = &addr
	}
	return p
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// EventCursor 是 auditEvents 分页的 keyset 游标：上一页最后一条的 (occur_time, id)。
type EventCursor struct {
	Time time.Time
	ID   string
}

// ListByUser 按 (occur_time, id) 升序返回 after 之后的至多 limit 条事件（after 为 nil 从头开始）。
// 读取不产生审计事件（§4.3）。
func (s *Store) ListByUser(ctx context.Context, userID string, after *EventCursor, limit int) ([]db.AuditEvent, error) {
	p := db.ListAuditEventsByUserParams{UserID: userID, PageLimit: int32(limit)}
	if after != nil {
		t, id := after.Time, after.ID
		p.AfterTime, p.AfterID = &t, &id
	}
	rows, err := s.q.ListAuditEventsByUser(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("audit: list events by user: %w", err)
	}
	return rows, nil
}

// DeleteOlderThan 分批删除 occur_time < before 的事件，删到一批不满为止；返回删除总数。
// 维护任务 audit_retention 每轮调用一次。
func (s *Store) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := s.q.DeleteAuditEventsBefore(ctx, db.DeleteAuditEventsBeforeParams{Before: before, BatchSize: RetentionBatchSize})
		if err != nil {
			return total, fmt.Errorf("audit: delete expired events: %w", err)
		}
		total += n
		if n < RetentionBatchSize {
			return total, nil
		}
	}
}
