package user

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/bbxx111/accountkit/internal/userdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/puddle/v2"
)

// setSessionAuthenticationIdentities replaces current authentication sources.
// The caller holds the account/session locks and supplies verified concrete identities.
func (s *Service) setSessionAuthenticationIdentities(ctx context.Context, q *db.Queries, sess db.Session, identityIDs []string) error {
	if len(identityIDs) == 0 {
		return fmt.Errorf("user: invariant violated: empty authentication identities")
	}
	identities, err := q.ListActiveIdentitiesByUser(ctx, sess.UserID)
	if err != nil {
		return fmt.Errorf("user: load authentication identities: %w", err)
	}
	active := make(map[string]bool, len(identities))
	for _, identity := range identities {
		if identity.Kind.Valid() && s.policy.Allows(identity.Kind) {
			active[identity.ID] = true
		}
	}
	unique := make(map[string]bool, len(identityIDs))
	for _, id := range identityIDs {
		if !active[id] {
			return fmt.Errorf("user: invariant violated: invalid authentication identity reference")
		}
		unique[id] = true
	}
	if _, err = q.DeleteSessionAuthenticationIdentities(ctx, db.DeleteSessionAuthenticationIdentitiesParams{UserID: sess.UserID, SessionID: sess.ID}); err != nil {
		return fmt.Errorf("user: replace authentication identities: %w", err)
	}
	for id := range unique {
		n, err := q.AddSessionAuthenticationIdentity(ctx, db.AddSessionAuthenticationIdentityParams{UserID: sess.UserID, SessionID: sess.ID, IdentityID: id})
		if err != nil {
			return fmt.Errorf("user: add authentication identity: %w", err)
		}
		if n != 1 {
			return fmt.Errorf("user: invariant violated: authentication identity write rejected")
		}
	}
	return nil
}

// requireSessionAuthenticationIdentities reads current sources under the caller's
// session lock. A missing set is corrupt state, never an unknown compatibility state.
func (s *Service) requireSessionAuthenticationIdentities(ctx context.Context, q *db.Queries, sess db.Session) ([]db.ListSessionAuthenticationIdentitiesRow, error) {
	rows, err := q.ListSessionAuthenticationIdentities(ctx, db.ListSessionAuthenticationIdentitiesParams{UserID: sess.UserID, SessionID: sess.ID})
	if err != nil {
		return nil, fmt.Errorf("user: read current authentication identities: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("user: invariant violated: empty authentication identities")
	}
	for _, row := range rows {
		if !row.Kind.Valid() {
			return nil, fmt.Errorf("user: invariant violated: invalid authentication identity kind")
		}
	}
	return rows, nil
}

// GetCurrentSession reads the latest database context while holding FOR SHARE
// until every field has been read, including under READ COMMITTED isolation.
func (s *Service) GetCurrentSession(ctx context.Context, p Principal) (CurrentSessionInfo, error) {
	if p.Scope != ScopeUser && p.Scope != ScopeBind {
		return CurrentSessionInfo{}, ErrInsufficientScope
	}
	var result CurrentSessionInfo
	err := s.d.Repo.withDomainTx(ctx, func(q *db.Queries) error {
		sess, err := q.LockCurrentSessionForRead(ctx, db.LockCurrentSessionForReadParams{UserID: p.UserID, SessionID: p.SessionID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidToken
		}
		if err != nil {
			return fmt.Errorf("user: read current session: %w", err)
		}
		if !sessionActiveAt(sess, s.now()) {
			return ErrInvalidToken
		}
		result = CurrentSessionInfo{
			SessionInfo:              SessionInfo{ID: sess.ID, DeviceID: sess.DeviceID, CreateTime: sess.CreateTime, LastUsedTime: sess.LastUsedTime, IsCurrent: true},
			AuthTime:                 sess.AuthTime,
			AuthenticationIdentities: make([]AuthenticationIdentity, 0),
		}
		if sess.DeviceName != nil {
			result.DeviceName = *sess.DeviceName
		}
		if err := s.loadCurrentSessionClientInfo(ctx, q, p.UserID, &result); err != nil {
			return err
		}
		rows, err := s.requireSessionAuthenticationIdentities(ctx, q, sess)
		if err != nil {
			return err
		}
		for _, row := range rows {
			result.AuthenticationIdentities = append(result.AuthenticationIdentities, AuthenticationIdentity{ID: row.IdentityID, Kind: row.Kind})
		}
		return nil
	})
	if err != nil {
		return CurrentSessionInfo{}, sessionContextStorageError(err)
	}
	return result, nil
}

func sessionContextStorageError(err error) error {
	var connect *pgconn.ConnectError
	var network net.Error
	var database *pgconn.PgError
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, puddle.ErrClosedPool) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &connect) || errors.As(err, &network) ||
		(errors.As(err, &database) && (strings.HasPrefix(database.Code, "08") || strings.HasPrefix(database.Code, "53") || database.Code == "57P01" || database.Code == "57P02" || database.Code == "57P03")) {
		return fmt.Errorf("%w: session context storage: %w", ErrUnavailable, err)
	}
	return err
}
