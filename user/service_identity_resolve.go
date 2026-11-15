package user

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	db "github.com/bbxx111/accountkit/internal/userdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/puddle/v2"
)

// ResolveIdentityInput 由可信宿主提供，Actor 必须为合法、稳定的 SERVICE 主体。
type ResolveIdentityInput struct {
	Kind    enum.IdentityKind
	Subject string
	Actor   audit.Actor
	Meta    Meta
}

// ResolvedIdentity 是当前关联的最小账号快照，不构成业务授权证明。
type ResolvedIdentity struct {
	UserID      string
	State       enum.UserState
	DisplayName string
	AvatarURL   string
}

// ResolveIdentity 只读解析 PHONE/EMAIL，宿主负责调用授权；查询不依赖登录策略。
func (s *Service) ResolveIdentity(ctx context.Context, in ResolveIdentityInput) (out ResolvedIdentity, err error) {
	valid := func(v string) bool {
		return v != "" && len(v) <= 512 && utf8.ValidString(v) && !strings.ContainsRune(v, 0)
	}
	if in.Actor.Type != audit.ActorService || !valid(in.Actor.Namespace) || !valid(in.Actor.ID) {
		return out, ErrInvalidArgument
	}
	if in.Kind != enum.IdentityPhone && in.Kind != enum.IdentityEmail {
		return out, ErrInvalidArgument
	}
	subject, _, _, e := s.normalizeTarget(in.Kind, in.Subject)
	if e != nil {
		return out, ErrInvalidTarget
	}
	defer func() { s.recordIdentityResolve(ctx, in, out, err) }()
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, e := s.d.Repo.q.ResolveActiveIdentity(readCtx, db.ResolveActiveIdentityParams{Kind: in.Kind, Digests: s.d.Digester.AllDigests(subject)})
	if e != nil {
		var network net.Error
		var pg *pgconn.PgError
		var connect *pgconn.ConnectError
		if errors.Is(e, io.EOF) || errors.Is(e, io.ErrUnexpectedEOF) || readCtx.Err() != nil || errors.As(e, &network) || errors.As(e, &connect) || errors.Is(e, puddle.ErrClosedPool) || (errors.As(e, &pg) && (strings.HasPrefix(pg.Code, "08") || strings.HasPrefix(pg.Code, "53") || pg.Code == "55P03" || pg.Code == "25P04" || pg.Code == "57014" || pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03")) {
			return out, ErrUnavailable
		}
		return out, errors.New("user: identity resolution query failed")
	}
	if len(rows) == 0 {
		return out, ErrNotFound
	}
	if len(rows) != 1 || rows[0].ID == nil {
		return out, errors.New("user: identity resolution invariant violated")
	}
	row := rows[0]
	state := enum.UserState(row.State)
	if state != enum.UserActive && state != enum.UserFrozen && state != enum.UserPendingDeletion {
		return out, errors.New("user: identity resolution invariant violated")
	}
	out = ResolvedIdentity{UserID: *row.ID, State: state}
	if row.DisplayName != nil {
		out.DisplayName = *row.DisplayName
	}
	if row.AvatarUrl != nil {
		out.AvatarURL = *row.AvatarUrl
	}
	return out, nil
}

func (s *Service) recordIdentityResolve(ctx context.Context, in ResolveIdentityInput, out ResolvedIdentity, err error) {
	outcome := "resolved"
	result := enum.ResultSuccess
	if err != nil {
		result = enum.ResultFailure
		switch {
		case errors.Is(err, ErrNotFound):
			outcome = "not_found"
		case errors.Is(err, ErrUnavailable):
			outcome = "unavailable"
		default:
			outcome = "internal"
			s.d.Logger.ErrorContext(ctx, "identity resolution invariant or query failure")
		}
	}
	id, e := uuid.NewV7()
	if e != nil {
		s.d.Logger.WarnContext(ctx, "identity resolution audit event rejected", "reason", "id_generation")
		return
	}
	data, _ := json.Marshal(struct {
		Kind    string `json:"kind"`
		Outcome string `json:"outcome"`
	}{in.Kind.String(), outcome})
	actor := in.Actor
	actor.Name = ""
	ev := audit.Event{ID: id, Source: s.d.AuditSource, Action: "IDENTITY_RESOLVE", Result: &result, Actor: actor, OccurTime: s.now(), Data: data, Context: audit.EventContext{RequestID: in.Meta.RequestID}}
	if err == nil {
		ev.Targets = []audit.Resource{{Type: "USER", Namespace: s.d.AuditNamespace, ID: out.UserID}}
	}
	frozen, e := event.Freeze(ev)
	if e != nil {
		s.d.Logger.WarnContext(ctx, "identity resolution audit event rejected", "reason", "invalid_event")
		return
	}
	s.d.Audit.Record(ctx, frozen)
}
