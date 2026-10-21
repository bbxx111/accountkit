package user

import (
	"context"
	"encoding/json"

	"github.com/bbxx111/accountkit/audit"
	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

func (s *Service) recordProfileAttributesUpdated(ctx context.Context, id string, v ProfileAttributesView, requestID string) {
	identity, err := uuid.NewV7()
	if err != nil {
		s.d.Logger.Warn("user: profile_attributes audit identity unavailable")
		return
	}
	data, _ := json.Marshal(struct {
		Version     string `json:"schema_version"`
		Fingerprint string `json:"schema_fingerprint"`
	}{*v.SchemaVersion, *v.SchemaFingerprint})
	result := enum.ResultSuccess
	e, err := event.Freeze(audit.Event{ID: identity, Source: s.d.AuditSource, Action: "USER_PROFILE_ATTRIBUTES_UPDATED", Result: &result, Actor: audit.Actor{Type: audit.ActorService, Namespace: s.d.AuditNamespace, ID: "accountkit-host"}, Targets: []audit.Resource{{Type: "USER", Namespace: s.d.AuditNamespace, ID: id}}, Context: audit.EventContext{RequestID: requestID}, Data: data, OccurTime: s.now()})
	if err != nil {
		s.d.Logger.Warn("user: profile_attributes audit event rejected")
		return
	}
	s.d.Audit.Record(ctx, e)
}
