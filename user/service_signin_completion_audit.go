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
	reason := auditReasonInternal
	var limit *code.RateLimitedError
	switch {
	case errors.Is(err, ErrSignInCompletionInvalid):
		reason = auditReasonSignInCompletionInvalid
	case errors.Is(err, ErrSignInAlreadyCompleted):
		reason = auditReasonSignInAlreadyCompleted
	case errors.Is(err, code.ErrInvalid):
		reason = auditReasonCodeInvalid
	case errors.Is(err, code.ErrExpired):
		reason = auditReasonCodeExpired
	case errors.Is(err, ErrIdentityConflict):
		reason = auditReasonIdentityAlreadyBound
	case errors.Is(err, ErrIdentityKindLimit):
		reason = auditReasonIdentityKindLimit
	case errors.Is(err, ErrUserFrozen):
		reason = auditReasonUserFrozen
	case errors.Is(err, ErrUserPendingDeletion):
		reason = auditReasonUserPendingDeletion
	case errors.Is(err, ErrIdentityKindNotEnabled):
		reason = auditReasonIdentityKindNotEnabled
	case errors.Is(err, sender.ErrDisabled):
		reason = auditReasonChannelNotEnabled
	case errors.Is(err, sender.ErrTargetNotSupported):
		reason = auditReasonTargetNotSupported
	case errors.Is(err, ErrUnavailable), errors.Is(err, code.ErrUnavailable):
		reason = auditReasonUnavailable
	case errors.Is(err, ErrInvalidTarget), errors.Is(err, ErrInvalidArgument):
		reason = auditReasonInvalidArgument
	case errors.As(err, &limit):
		reason = auditReasonRateLimited
	}
	if !kind.Valid() {
		kind = enum.IdentityKindUnspecified
	}
	s.record(ctx, audit.AuthenticationEvent{Type: enum.EventSignInCompletionRejected, Result: enum.ResultFailure, Reason: reason, FlowID: flowID, IdentityKind: kind, IP: meta.IP, DeviceID: dev.ID, RequestID: meta.RequestID})
}
