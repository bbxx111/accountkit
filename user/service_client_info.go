package user

import (
	"context"
	"errors"
	"fmt"
	"github.com/bbxx111/accountkit/user/db"
	"github.com/jackc/pgx/v5"
	"time"
)

// Every write is in the session transaction with its row lock held.
func (s *Service) initializeSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, info *ClientInfo, observedAt, now time.Time) error {
	return s.writeSessionClientInfo(ctx, q, sess, info, observedAt, now)
}
func (s *Service) updateSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, observed *ClientInfo, now time.Time) error {
	return s.writeSessionClientInfo(ctx, q, sess, observed, now, now)
}
func (s *Service) writeSessionClientInfo(ctx context.Context, q *db.Queries, sess db.Session, info *ClientInfo, observedAt, now time.Time) error {
	normalized, err := NormalizeClientInfo(info)
	if err != nil {
		return fmt.Errorf("user: invalid validated client information")
	}
	if normalized == nil {
		return nil
	}
	encoded, err := MarshalClientInfo(normalized)
	if err != nil {
		return fmt.Errorf("user: encode validated client information")
	}
	n, err := q.UpsertSessionClientInfo(ctx, db.UpsertSessionClientInfoParams{UserID: sess.UserID, SessionID: sess.ID, ClientInfo: encoded, ObserveTime: observedAt, Now: now})
	if err != nil {
		return fmt.Errorf("user: write client information: %w", err)
	}
	if n != 1 {
		return fmt.Errorf("user: invariant violated: client information write rejected")
	}
	return nil
}
func clientInfoFrom(row db.SessionClientInfo) (*ClientInfo, error) {
	info, err := UnmarshalClientInfo(row.ClientInfo)
	if err != nil || info == nil {
		// Stored corruption is internal, not a client-input error. Never include content.
		return nil, fmt.Errorf("user: invariant violated: invalid stored client information")
	}
	return info, nil
}
func (s *Service) loadCurrentSessionClientInfo(ctx context.Context, q *db.Queries, userID string, result *CurrentSessionInfo) error {
	row, err := q.GetSessionClientInfo(ctx, db.GetSessionClientInfoParams{UserID: userID, SessionID: result.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("user: read current client information: %w", err)
	}
	result.ClientInfo, err = clientInfoFrom(row)
	if err != nil {
		return err
	}
	result.ClientInfoUpdateTime = &row.ObserveTime
	return nil
}
