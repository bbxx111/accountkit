package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
)

// 快照写入须与会话变更处于同一事务，并已持有该会话行锁（新建行亦可）。
// 调用者提供服务器判定时刻；这些字段不用于认证、设备匹配或业务活动追踪。
func (s *Service) initializeSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, initial *ClientInfo, observedAt, now time.Time) error {
	snapshot := MergeClientInfo(nil, initial)
	if snapshot == nil {
		return nil
	}
	if err := s.writeSessionClientInfo(ctx, q, sess, enum.ClientSnapshotInitial, snapshot, observedAt, now); err != nil {
		return err
	}
	return s.writeSessionClientInfo(ctx, q, sess, enum.ClientSnapshotLatest, snapshot, observedAt, now)
}

func (s *Service) updateSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, observed *ClientInfo, now time.Time) error {
	if observed == nil {
		return nil
	}
	row, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: sess.UserID, SessionID: sess.ID, SnapshotKind: enum.ClientSnapshotLatest})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("user: read client snapshot: %w", err)
	}
	var previous *ClientInfo
	if err == nil {
		previous = clientInfoFrom(row)
	}
	merged := MergeClientInfo(previous, observed)
	if !hasEffectiveClientObservation(merged, observed) {
		return nil
	}
	return s.writeSessionClientInfo(ctx, q, sess, enum.ClientSnapshotLatest, merged, now, now)
}

func hasEffectiveClientObservation(merged, observed *ClientInfo) bool {
	if merged == nil || observed == nil {
		return false
	}
	effective := *observed
	if merged.OSName == "" {
		effective.OSVersion = ""
	}
	if merged.BrowserName == "" {
		effective.BrowserVersion = ""
	}
	if merged.AppName == "" {
		effective.AppVersion = ""
	}
	if merged.ClientType == enum.ClientNative {
		effective.BrowserName = ""
		effective.BrowserVersion = ""
	}
	if merged.ClientType == enum.ClientWeb {
		effective.AppName = ""
		effective.AppVersion = ""
	}
	return effective != (ClientInfo{})
}

func (s *Service) writeSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, kind enum.ClientInfoSnapshotKind, info *ClientInfo, observedAt, now time.Time) error {
	p := db.UpsertSessionClientInfoParams{UserID: sess.UserID, SessionID: sess.ID, SnapshotKind: kind, ObserveTime: observedAt, Now: now,
		OsName: clientOptional(info.OSName), OsVersion: clientOptional(info.OSVersion), BrowserName: clientOptional(info.BrowserName), BrowserVersion: clientOptional(info.BrowserVersion), AppName: clientOptional(info.AppName), AppVersion: clientOptional(info.AppVersion), DeviceModel: clientOptional(info.DeviceModel)}
	if info.ClientType != 0 {
		v := info.ClientType
		p.ClientType = &v
	}
	if info.DeviceType != 0 {
		v := info.DeviceType
		p.DeviceType = &v
	}
	n, err := q.UpsertSessionClientInfo(ctx, p)
	if err != nil {
		return fmt.Errorf("user: write client snapshot: %w", err)
	}
	// INITIAL 冲突表示快照保持不变，零行不是失败。
	if n != 1 && kind != enum.ClientSnapshotInitial {
		return fmt.Errorf("user: invariant violated: client snapshot write rejected")
	}
	return nil
}

func clientOptional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func clientValue[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}
	return *v
}
func clientInfoFrom(row db.SessionClientInfo) *ClientInfo {
	return &ClientInfo{ClientType: clientValue(row.ClientType), DeviceType: clientValue(row.DeviceType), OSName: clientValue(row.OsName), OSVersion: clientValue(row.OsVersion), BrowserName: clientValue(row.BrowserName), BrowserVersion: clientValue(row.BrowserVersion), AppName: clientValue(row.AppName), AppVersion: clientValue(row.AppVersion), DeviceModel: clientValue(row.DeviceModel)}
}

func (s *Service) loadCurrentSessionClientInfo(ctx context.Context, q *db.Queries, userID string, result *CurrentSessionInfo) error {
	for _, kind := range []enum.ClientInfoSnapshotKind{enum.ClientSnapshotInitial, enum.ClientSnapshotLatest} {
		row, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: userID, SessionID: result.ID, SnapshotKind: kind})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("user: read current client snapshot: %w", err)
		}
		if kind == enum.ClientSnapshotInitial {
			result.InitialClientInfo = clientInfoFrom(row)
		} else {
			result.ClientInfo = clientInfoFrom(row)
			result.ClientInfoUpdateTime = &row.ObserveTime
		}
	}
	return nil
}
