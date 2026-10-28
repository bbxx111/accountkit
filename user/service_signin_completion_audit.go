package user

import (
	"context"
	"errors"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/sender"
)

func (s *Service) recordCompletionRejected(ctx context.Context, token, flowID string, kind enum.IdentityKind, dev Device, meta Meta, err error) {
	// Parameter/policy rejection precedes credential consumption. Resolve only
	// the already stored internal ID so these attempts retain flow correlation.
	if flowID == "" && (errors.Is(err, ErrInvalidArgument) || errors.Is(err, ErrInvalidTarget) || errors.Is(err, ErrIdentityKindNotEnabled)) {
		row, _, _ := s.loadFlow(ctx, token, Device{})
		flowID = row.ID
	}
	reason := "INTERNAL"
	var limit *code.RateLimitedError
	switch {
	case errors.Is(err, ErrSignInCompletionInvalid):
		reason = "SIGN_IN_COMPLETION_INVALID"
	case errors.Is(err, ErrSignInAlreadyCompleted):
		reason = "SIGN_IN_ALREADY_COMPLETED"
	case errors.Is(err, code.ErrInvalid):
		reason = "CODE_INVALID"
	case errors.Is(err, code.ErrExpired):
		reason = "CODE_EXPIRED"
	case errors.Is(err, code.ErrExhausted):
		reason = "CODE_ATTEMPTS_EXHAUSTED"
	case errors.Is(err, ErrIdentityConflict):
		reason = "IDENTITY_ALREADY_BOUND"
	case errors.Is(err, ErrIdentityKindLimit):
		reason = "IDENTITY_KIND_LIMIT"
	case errors.Is(err, ErrUserFrozen):
		reason = "USER_FROZEN"
	case errors.Is(err, ErrUserPendingDeletion):
		reason = "USER_PENDING_DELETION"
	case errors.Is(err, ErrIdentityKindNotEnabled):
		reason = "IDENTITY_KIND_NOT_ENABLED"
	case errors.Is(err, sender.ErrDisabled):
		reason = "CHANNEL_NOT_ENABLED"
	case errors.Is(err, sender.ErrTargetNotSupported):
		reason = "TARGET_NOT_SUPPORTED"
	case errors.Is(err, ErrUnavailable), errors.Is(err, code.ErrUnavailable):
		reason = "UNAVAILABLE"
	case errors.Is(err, ErrInvalidTarget), errors.Is(err, ErrInvalidArgument):
		reason = "INVALID_ARGUMENT"
	case errors.As(err, &limit):
		reason = "RATE_LIMITED"
	}
	if !kind.Valid() {
		kind = enum.IdentityKindUnspecified
	}
	s.record(ctx, audit.AuthenticationEvent{Type: enum.EventSignInCompletionRejected, Result: enum.ResultFailure, Reason: reason, FlowID: flowID, IdentityKind: kind, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID})
}
