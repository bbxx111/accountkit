package authentication

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

var options = Options{Source: "accountkit/test", Namespace: "urn:test:accounts"}

func fixture(typ enum.EventType) Input {
	return Input{Type: typ, Actor: enum.ActorUser, Result: enum.ResultSuccess, Reason: "SAFE_REASON", UserID: "u_test", SessionID: "s_test", IdentityID: "i_test", FlowID: "f_test", IdentityKind: enum.IdentityPhone, SubjectHint: "12345678", IP: "192.0.2.1", DeviceID: "device", RequestID: "request", OccurTime: time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)}
}

func refs(resources []event.Resource) []string {
	var out []string
	for _, r := range resources {
		out = append(out, r.Type+":"+r.ID)
		if r.Namespace != options.Namespace {
			panic("unexpected namespace")
		}
	}
	return out
}

func TestAuthenticationEventMapping(t *testing.T) {
	// These expectations describe each action's affected object, separately from
	// background account/session/flow resources. An identity is only supplied once known.
	cases := []struct {
		typ              enum.EventType
		actor            enum.ActorKind
		generic          event.ActorType
		targets, related []string
	}{
		{enum.EventCodeSent, enum.ActorUser, event.ActorUser, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}, []string{"SESSION:s_test"}},
		{enum.EventCodeSendRejected, enum.ActorUser, event.ActorUser, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}, []string{"SESSION:s_test"}},
		{enum.EventCodeSendFailed, enum.ActorUser, event.ActorUser, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}, []string{"SESSION:s_test"}},
		{enum.EventSignIn, enum.ActorUser, event.ActorUser, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventSignInFailed, enum.ActorUser, event.ActorUser, nil, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventReauthenticated, enum.ActorUser, event.ActorUser, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventReauthenticationFailed, enum.ActorUser, event.ActorUser, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventTokenRefreshed, enum.ActorUser, event.ActorUser, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventRefreshRejected, enum.ActorUser, event.ActorUnknown, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventRefreshReuseDetected, enum.ActorUser, event.ActorUnknown, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventSessionRevoked, enum.ActorUser, event.ActorUser, []string{"SESSION:s_test"}, []string{"IDENTITY_FLOW:f_test", "USER:u_test"}},
		{enum.EventIdentityBound, enum.ActorUser, event.ActorUser, []string{"IDENTITY:i_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventIdentityBindRejected, enum.ActorUser, event.ActorUser, []string{"IDENTITY:i_test", "USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventIdentityUnbound, enum.ActorUser, event.ActorUser, []string{"IDENTITY:i_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventIdentityRevealed, enum.ActorAdmin, event.ActorUser, []string{"IDENTITY:i_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventUserDeleted, enum.ActorUser, event.ActorUser, []string{"USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventUserUndeleted, enum.ActorUser, event.ActorUser, []string{"USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventUserPurged, enum.ActorSystem, event.ActorSystem, []string{"USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventUserFrozen, enum.ActorAdmin, event.ActorUser, []string{"USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventUserUnfrozen, enum.ActorAdmin, event.ActorUser, []string{"USER:u_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test"}},
		{enum.EventAdminForbidden, enum.ActorAdmin, event.ActorUser, nil, nil},
		{enum.EventIdentityReplaceRejected, enum.ActorUser, event.ActorUser, []string{"IDENTITY:i_test"}, []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventSignInCompletionStarted, enum.ActorUser, event.ActorAnonymous, []string{"IDENTITY_FLOW:f_test"}, []string{"SESSION:s_test", "USER:u_test"}},
		{enum.EventSignInCompletionProofVerified, enum.ActorUser, event.ActorUser, []string{"IDENTITY_FLOW:f_test"}, []string{"IDENTITY:i_test", "SESSION:s_test", "USER:u_test"}},
		{enum.EventSignInCompletionCompleted, enum.ActorUser, event.ActorUser, []string{"IDENTITY_FLOW:f_test"}, []string{"SESSION:s_test", "USER:u_test"}},
		{enum.EventSignInCompletionRejected, enum.ActorUser, event.ActorUnknown, []string{"IDENTITY_FLOW:f_test"}, []string{"SESSION:s_test", "USER:u_test"}},
	}
	if len(cases) != 26 {
		t.Fatal("mapping inventory incomplete")
	}
	for _, tc := range cases {
		t.Run(tc.typ.String(), func(t *testing.T) {
			in := fixture(tc.typ)
			in.Actor = tc.actor
			in.VerifiedSubject = true
			if tc.actor == enum.ActorAdmin {
				in.AdminIssuer = "https://admin.example/"
				in.AdminSubject = "subject"
				in.AdminUsername = "operator"
			}
			if tc.typ == enum.EventAdminForbidden {
				in.UserID = ""
				in.SessionID = ""
				in.FlowID = ""
				in.IdentityID = ""
			}
			e, err := Map(in, options)
			if err != nil {
				t.Fatal(err)
			}
			if e.ID.Version() != 7 || e.Source != options.Source || e.Action != tc.typ.String() || e.Actor.Type != tc.generic || e.Result == nil || *e.Result != in.Result || !e.OccurTime.Equal(in.OccurTime) {
				t.Fatalf("incorrect envelope: %+v", e)
			}
			if !reflect.DeepEqual(refs(e.Targets), tc.targets) || !reflect.DeepEqual(refs(e.Context.RelatedResources), tc.related) {
				t.Fatalf("targets=%v related=%v", refs(e.Targets), refs(e.Context.RelatedResources))
			}
			if tc.actor == enum.ActorAdmin && (e.Actor.Namespace != in.AdminIssuer || e.Actor.ID != in.AdminSubject) {
				t.Fatalf("admin identity lost: %+v", e.Actor)
			}
			if tc.generic == event.ActorUser && tc.actor == enum.ActorUser && (e.Actor.Namespace != options.Namespace || e.Actor.ID != in.UserID) {
				t.Fatalf("user identity lost: %+v", e.Actor)
			}
			projected, err := Projection(e)
			if err != nil {
				t.Fatal(err)
			}
			if projected.Type != in.Type || projected.Actor != in.Actor || projected.Result != in.Result || projected.Reason != in.Reason || projected.UserID != in.UserID || projected.SessionID != in.SessionID || projected.FlowID != in.FlowID || projected.IdentityKind != in.IdentityKind || projected.SubjectHint != in.SubjectHint || projected.IP != in.IP || projected.DeviceID != in.DeviceID || projected.RequestID != in.RequestID || projected.AdminIssuer != in.AdminIssuer || projected.AdminSubject != in.AdminSubject || projected.AdminUsername != in.AdminUsername {
				t.Fatalf("projection mismatch: %+v", projected)
			}
		})
	}
}

func TestUnverifiedSubjectDoesNotBecomeActor(t *testing.T) {
	for _, typ := range []enum.EventType{enum.EventSignInFailed, enum.EventRefreshRejected, enum.EventRefreshReuseDetected, enum.EventSignInCompletionProofVerified, enum.EventSignInCompletionRejected, enum.EventIdentityReplaceRejected} {
		t.Run(typ.String(), func(t *testing.T) {
			in := fixture(typ)
			e, err := Map(in, options)
			if err != nil {
				t.Fatal(err)
			}
			if e.Actor.Type == event.ActorUser || e.Actor.Namespace != "" || e.Actor.ID != "" {
				t.Fatalf("target became credential holder: %+v", e.Actor)
			}
		})
	}
	for _, typ := range []enum.EventType{enum.EventCodeSent, enum.EventCodeSendRejected, enum.EventCodeSendFailed, enum.EventSignInFailed, enum.EventRefreshRejected, enum.EventSignInCompletionStarted, enum.EventSignInCompletionRejected} {
		in := fixture(typ)
		in.UserID = ""
		in.SessionID = ""
		in.FlowID = ""
		in.IdentityID = ""
		e, err := Map(in, options)
		if err != nil {
			t.Fatal(err)
		}
		if e.Actor.Type != event.ActorAnonymous {
			t.Fatalf("%s: %+v", typ, e.Actor)
		}
	}
}

type memory struct{ events []event.Event }

func (m *memory) Record(_ context.Context, e event.Event) { m.events = append(m.events, e) }

func TestAuthenticationIDFailureDoesNotChangeResult(t *testing.T) {
	var logs bytes.Buffer
	m := &memory{}
	r, err := NewRecorder(m, options, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r.newID = func() (uuid.UUID, error) { return uuid.Nil, errors.New("secret credential raw error") }
	// A void auditing call cannot replace the authentication decision.
	result := "authenticated"
	r.Record(context.Background(), fixture(enum.EventSignIn))
	if result != "authenticated" || len(m.events) != 0 || r.Stats().Failed != 1 {
		t.Fatalf("result=%s events=%d stats=%+v", result, len(m.events), r.Stats())
	}
	if strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), "id_generation") {
		t.Fatalf("unsafe or missing failure log: %s", logs.String())
	}
}

func TestAuthenticationOptionalFieldsAndStableIdentityLimits(t *testing.T) {
	var logs bytes.Buffer
	m := &memory{}
	r, err := NewRecorder(m, options, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatal(err)
	}
	in := fixture(enum.EventUserFrozen)
	in.Actor = enum.ActorAdmin
	in.AdminIssuer = "https://admin.example/"
	in.AdminSubject = "admin"
	in.AdminUsername = strings.Repeat("名", 257)
	in.IP = "invalid-secret-ip"
	in.DeviceID = strings.Repeat("d", 257)
	in.RequestID = strings.Repeat("r", 129)
	r.Record(context.Background(), in)
	if len(m.events) != 1 {
		t.Fatalf("optional fields discarded event: %+v", r.Stats())
	}
	e := m.events[0]
	if e.Actor.Name != "" || e.Context.ClientIP != "" || e.Context.DeviceID != "" || e.Context.RequestID != "" || r.Stats().Omitted != 4 {
		t.Fatalf("unsafe optional fields: %+v stats=%+v", e, r.Stats())
	}
	in.AdminSubject = strings.Repeat("s", 513)
	r.Record(context.Background(), in)
	if len(m.events) != 1 || r.Stats().Failed != 1 {
		t.Fatalf("overlong known identity accepted: %+v", r.Stats())
	}
	if strings.Contains(logs.String(), "invalid-secret-ip") || strings.Contains(logs.String(), in.AdminSubject) {
		t.Fatalf("unsafe logs: %s", logs.String())
	}
	for _, s := range []string{strings.Repeat("名", 256), strings.Repeat("a", 512)} {
		in.AdminSubject = "admin"
		in.AdminUsername = s
		if _, err := Map(in, options); err != nil {
			t.Fatalf("optional name should be omitted, not fail: %v", err)
		}
	}
}

func TestAuthenticationScrub(t *testing.T) {
	in := fixture(enum.EventIdentityRevealed)
	in.Actor = enum.ActorAdmin
	in.AdminIssuer = "issuer"
	in.AdminSubject = "subject"
	in.AdminUsername = "name"
	e, err := Map(in, options)
	if err != nil {
		t.Fatal(err)
	}
	e.Context.TraceID = strings.Repeat("a", 32)
	e.Context.SpanID = strings.Repeat("b", 16)
	scrubbed := Scrub(e)
	p, err := Projection(scrubbed)
	if err != nil {
		t.Fatal(err)
	}
	if p.SubjectHint != "" || p.IP != "" || p.DeviceID != "" || p.AdminUsername != "" || p.AdminIssuer != "issuer" || p.AdminSubject != "subject" || p.RequestID != "request" || scrubbed.Context.TraceID != e.Context.TraceID || scrubbed.Context.SpanID != e.Context.SpanID {
		t.Fatalf("incorrect scrub: %+v", p)
	}
	if bytes.Equal(e.Data, scrubbed.Data) || e.Actor.Name != "name" || e.Context.ClientIP == "" {
		t.Fatal("scrub mutated original or failed to clear hint")
	}
	var data map[string]json.RawMessage
	if err := json.Unmarshal(scrubbed.Data, &data); err != nil {
		t.Fatal(err)
	}
	if string(data["reason"]) != "\"SAFE_REASON\"" {
		t.Fatal("scrub removed non-private data")
	}
	if _, err := Projection(event.Event{Action: "ORDER_CREATED"}); err == nil {
		t.Fatal("business event projected as authentication")
	}
}

func TestAuthenticationStableIDAndLegacyAdministrator(t *testing.T) {
	stable, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	in := fixture(enum.EventSignIn)
	in.ID = stable
	e, err := Map(in, options)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Projection(e)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != stable || p.ID != stable {
		t.Fatal("retry regenerated event identity")
	}
	in.ID = uuid.New()
	if _, err := Map(in, options); err == nil {
		t.Fatal("non-v7 identity accepted")
	}
	for _, tc := range []struct{ issuer, subject string }{{"issuer", ""}, {"", "subject"}, {"", ""}} {
		in := fixture(enum.EventUserFrozen)
		in.Actor = enum.ActorAdmin
		in.AdminIssuer = tc.issuer
		in.AdminSubject = tc.subject
		in.AdminUsername = "admin name"
		if _, err := Map(in, options); err == nil {
			t.Fatal("new incomplete administrator accepted")
		}
		e, err := MapLegacy(in, options, "e_legacy")
		if err != nil {
			t.Fatal(err)
		}
		if e.Actor.Type != event.ActorUnknown || e.Actor.Namespace != "" || e.Actor.ID != "" {
			t.Fatalf("legacy missing identity fabricated: %+v", e.Actor)
		}
		p, err := Projection(e)
		if err != nil {
			t.Fatal(err)
		}
		if p.AdminIssuer != tc.issuer || p.AdminSubject != tc.subject || p.AdminUsername != "admin name" {
			t.Fatalf("legacy projection lost saved fields: %+v", p)
		}
		p, err = Projection(Scrub(e))
		if err != nil {
			t.Fatal(err)
		}
		if p.AdminUsername != "" || p.AdminIssuer != tc.issuer || p.AdminSubject != tc.subject {
			t.Fatalf("legacy scrub destroyed stable identity: %+v", p)
		}
		in.AdminIssuer = strings.Repeat("x", 513)
		if _, err := MapLegacy(in, options, "e_legacy"); err == nil {
			t.Fatal("legacy bypassed namespace limit")
		}
	}
}

func TestAuthenticationInvalidInputs(t *testing.T) {
	if _, err := NewRecorder(nil, options, nil); err == nil {
		t.Fatal("nil recorder accepted")
	}
	for _, o := range []Options{{Source: "", Namespace: "n"}, {Source: "source", Namespace: ""}, {Source: "秘密", Namespace: "n"}} {
		if _, err := NewRecorder(&memory{}, o, nil); err == nil {
			t.Fatal("invalid options accepted")
		}
	}
	for _, mutate := range []func(*Input){func(in *Input) { in.Type = enum.EventTypeUnspecified }, func(in *Input) { in.Actor = 99 }, func(in *Input) { in.Result = 99 }, func(in *Input) { in.IdentityKind = 99 }, func(in *Input) { in.UserID = strings.Repeat("u", 513) }, func(in *Input) { in.Reason = "bad\x00reason" }} {
		in := fixture(enum.EventSignIn)
		mutate(&in)
		if _, err := Map(in, options); err == nil {
			t.Fatal("invalid event accepted")
		}
	}
	// Names are Unicode-character bounded, while default HTTP device fields remain byte bounded.
	in := fixture(enum.EventUserFrozen)
	in.Actor = enum.ActorAdmin
	in.AdminIssuer = "issuer"
	in.AdminSubject = "subject"
	in.AdminUsername = strings.Repeat("名", 256)
	in.DeviceID = strings.Repeat("d", 64)
	e, err := Map(in, options)
	if err != nil {
		t.Fatal(err)
	}
	if e.Actor.Name != in.AdminUsername || e.Context.DeviceID != in.DeviceID {
		t.Fatal("valid current inputs were tightened")
	}
}

func TestAuthenticationProjectionRejectsUntrustedSnapshots(t *testing.T) {
	for _, wire := range []string{
		`{"accountkit":{"actor_kind":"USER","subject_hint":"full-secret-subject"}}`,
		`{"accountkit":{"actor_kind":"USER","flow_id":"f_unrelated"}}`,
		`{"accountkit":{"actor_kind":"USER","session_id":"s_unrelated"}}`,
		`{"accountkit":{"actor_kind":"ADMIN"}}`,
	} {
		e, err := Map(fixture(enum.EventSignIn), options)
		if err != nil {
			t.Fatal(err)
		}
		e.Data = json.RawMessage(wire)
		if _, err := Projection(e); err == nil {
			t.Fatalf("unsafe auth snapshot accepted: %s", wire)
		}
	}
}

func TestAuthenticationActorKindMustMatchAction(t *testing.T) {
	for _, kind := range []enum.ActorKind{enum.ActorAdmin, enum.ActorSystem} {
		in := fixture(enum.EventRefreshReuseDetected)
		in.Actor = kind
		in.AdminIssuer = "issuer"
		in.AdminSubject = "subject"
		if _, err := Map(in, options); err == nil {
			t.Fatal("refresh caller elevated via actor_kind")
		}
	}
}

func TestAuthenticationLegacyRejectsIncompatibleOptionalFields(t *testing.T) {
	in := fixture(enum.EventSignIn)
	in.RequestID = strings.Repeat("r", 129)
	if _, err := MapLegacy(in, options, "e_legacy"); err == nil {
		t.Fatal("history silently discarded incompatible request_id")
	}
}

func TestAuthenticationLegacyAdminForbiddenPreservesResources(t *testing.T) {
	for _, tc := range []struct {
		name, userID, sessionID, flowID string
		related                         []string
	}{
		{"user", "u_test", "", "", []string{"USER:u_test"}},
		{"session", "", "s_test", "", []string{"SESSION:s_test"}},
		{"flow", "", "", "f_test", []string{"IDENTITY_FLOW:f_test"}},
		{"all", "u_test", "s_test", "f_test", []string{"IDENTITY_FLOW:f_test", "SESSION:s_test", "USER:u_test"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture(enum.EventAdminForbidden)
			in.Actor = enum.ActorAdmin
			in.AdminIssuer = "issuer"
			in.AdminSubject = "subject"
			in.UserID = tc.userID
			in.SessionID = tc.sessionID
			in.FlowID = tc.flowID
			in.IdentityID = ""
			e, err := MapLegacy(in, options, "e_legacy")
			if err != nil {
				t.Fatal(err)
			}
			if len(e.Targets) != 0 || !reflect.DeepEqual(refs(e.Context.RelatedResources), tc.related) {
				t.Fatalf("saved historical resources lost: targets=%v related=%v", refs(e.Targets), refs(e.Context.RelatedResources))
			}
			p, err := Projection(e)
			if err != nil {
				t.Fatal(err)
			}
			if p.UserID != tc.userID || p.SessionID != tc.sessionID || p.FlowID != tc.flowID {
				t.Fatalf("historical fields lost: %+v", p)
			}
			online, err := Map(in, options)
			if err != nil {
				t.Fatal(err)
			}
			if len(online.Targets) != 0 || len(online.Context.RelatedResources) != 0 {
				t.Fatal("online ADMIN_FORBIDDEN inferred request targets")
			}
		})
	}
}
