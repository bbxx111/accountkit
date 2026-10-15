// Package authentication 将认证领域的强类型事件映射为通用审计事件。
package authentication

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/audit/event"
	"github.com/bbxx111/accountkit/enum"
	"github.com/google/uuid"
)

// Input 仅接收已脱敏的认证事实，不接受请求体、令牌或原始错误。
type Input struct {
	ID        uuid.UUID
	Type      enum.EventType
	Actor     enum.ActorKind
	Result    enum.Result
	Reason    string
	UserID    string
	SessionID string
	// IdentityID 仅填写已确认归属的身份，不从 kind/hint 推测。
	IdentityID    string
	FlowID        string
	IdentityKind  enum.IdentityKind
	SubjectHint   string
	IP            string
	DeviceID      string
	RequestID     string
	AdminIssuer   string
	AdminSubject  string
	AdminUsername string
	OccurTime     time.Time
	// VerifiedSubject 明确证明请求者是 UserID 所指主体；不能从资源归属推断。
	VerifiedSubject bool
}

// Options 由可信装配配置指定，映射器不猜测来源或命名空间。
type Options struct{ Source, Namespace string }

var sourcePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
var errIDGeneration = errors.New("audit/authentication: id_generation")

// Validate 校验配置，不将原配置值包含在错误中。
func (o Options) Validate() error {
	if len(o.Source) > 128 || !sourcePattern.MatchString(o.Source) {
		return errors.New("audit/authentication: invalid source")
	}
	if o.Namespace == "" || !validText(o.Namespace, 512) {
		return errors.New("audit/authentication: invalid namespace")
	}
	return nil
}

// Map 生成首次投递身份并返回独立、校验后的事件。已有 UUIDv7 保持不变。
// 业务调用应使用 Recorder，以获得失败和可选字段省略的安全观测。
func Map(in Input, o Options) (event.Event, error) {
	e, _, err := mapEvent(in, o, uuid.NewV7, "")
	return e, err
}

// MapLegacy 仅供受控历史预备转换；缺失管理员字段保持 UNKNOWN，
// 以显式旧事件标识标记受控快照，不能伪造已验证主体。
// 不兼容的历史可选字段必须停止转换，不沿用在线事件的安全省略策略。
func MapLegacy(in Input, o Options, legacyID string) (event.Event, error) {
	if legacyID == "" || !validText(legacyID, 512) {
		return event.Event{}, errors.New("audit/authentication: invalid legacy_event_id")
	}
	e, omitted, err := mapEvent(in, o, uuid.NewV7, legacyID)
	if err == nil && len(omitted) > 0 {
		return event.Event{}, errors.New("audit/authentication: incompatible legacy optional field")
	}
	return e, err
}

type snapshot struct {
	ActorKind     string `json:"actor_kind"`
	IdentityKind  string `json:"identity_kind,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	FlowID        string `json:"flow_id,omitempty"`
	SubjectHint   string `json:"subject_hint,omitempty"`
	LegacyEventID string `json:"legacy_event_id,omitempty"`
	AdminIssuer   string `json:"admin_issuer,omitempty"`
	AdminSubject  string `json:"admin_subject,omitempty"`
}
type data struct {
	Reason     string   `json:"reason,omitempty"`
	Accountkit snapshot `json:"accountkit"`
}

func mapEvent(in Input, o Options, newID func() (uuid.UUID, error), legacyID string) (event.Event, []string, error) {
	if err := o.Validate(); err != nil {
		return event.Event{}, nil, err
	}
	if !in.Type.Valid() || (in.Actor != enum.ActorKindUnspecified && !in.Actor.Valid()) || (in.Result != enum.ResultUnspecified && !in.Result.Valid()) || (in.IdentityKind != enum.IdentityKindUnspecified && !in.IdentityKind.Valid()) {
		return event.Event{}, nil, errors.New("audit/authentication: invalid enum")
	}
	if in.Actor == enum.ActorKindUnspecified {
		in.Actor = enum.ActorUser
	}
	if legacyID == "" && !validActorKind(in.Type, in.Actor) {
		return event.Event{}, nil, errors.New("audit/authentication: invalid actor for action")
	}
	if !validText(in.Reason, event.MaxEventBytes) {
		return event.Event{}, nil, errors.New("audit/authentication: invalid reason")
	}
	for _, s := range []string{in.UserID, in.SessionID, in.IdentityID, in.FlowID, in.AdminIssuer, in.AdminSubject} {
		if !validText(s, 512) {
			return event.Event{}, nil, errors.New("audit/authentication: invalid stable identity")
		}
	}
	if in.Actor == enum.ActorAdmin && legacyID == "" && (in.AdminIssuer == "" || in.AdminSubject == "") {
		return event.Event{}, nil, errors.New("audit/authentication: incomplete administrator")
	}
	var omitted []string
	optional := func(s string, max int, field string) string {
		if !validText(s, max) {
			omitted = append(omitted, field)
			return ""
		}
		return s
	}
	in.RequestID = optional(in.RequestID, 128, "request_id")
	in.DeviceID = optional(in.DeviceID, 256, "device_id")
	in.SubjectHint = optional(in.SubjectHint, 8, "subject_hint")
	if !validText(in.AdminUsername, 256*utf8.UTFMax) || utf8.RuneCountInString(in.AdminUsername) > 256 {
		in.AdminUsername = ""
		omitted = append(omitted, "actor_name")
	}
	if in.IP != "" {
		ip, err := netip.ParseAddr(in.IP)
		if err != nil || ip.Zone() != "" {
			in.IP = ""
			omitted = append(omitted, "client_ip")
		}
	}
	if in.OccurTime.IsZero() {
		in.OccurTime = time.Now()
	}
	if in.ID == uuid.Nil {
		id, err := newID()
		if err != nil {
			return event.Event{}, omitted, errIDGeneration
		}
		in.ID = id
	}
	e := event.Event{ID: in.ID, Source: o.Source, Action: in.Type.String(), Actor: actor(in, o), OccurTime: in.OccurTime, Context: event.EventContext{RequestID: in.RequestID, ClientIP: in.IP, DeviceID: in.DeviceID}}
	if in.Result.Valid() {
		r := in.Result
		e.Result = &r
	}
	// Keep every known account/flow reference queryable without treating ownership as proof.
	target := map[string]bool{}
	add := func(typ, id string, direct bool) {
		if id == "" {
			return
		}
		r := event.Resource{Namespace: o.Namespace, Type: typ, ID: id}
		if direct {
			e.Targets = append(e.Targets, r)
			target[typ+":"+id] = true
		} else if !target[typ+":"+id] {
			e.Context.RelatedResources = append(e.Context.RelatedResources, r)
		}
	}
	directSession := false
	switch in.Type {
	case enum.EventCodeSent, enum.EventCodeSendRejected, enum.EventCodeSendFailed:
		add("USER", in.UserID, true)
		add("IDENTITY_FLOW", in.FlowID, true)
	case enum.EventSignIn, enum.EventReauthenticated, enum.EventReauthenticationFailed, enum.EventTokenRefreshed, enum.EventRefreshRejected, enum.EventRefreshReuseDetected, enum.EventSessionRevoked:
		add("SESSION", in.SessionID, true)
		directSession = true
	case enum.EventIdentityBound, enum.EventIdentityUnbound, enum.EventIdentityRevealed, enum.EventIdentityReplaceRejected:
		add("IDENTITY", in.IdentityID, true)
	case enum.EventIdentityBindRejected:
		add("USER", in.UserID, true)
		add("IDENTITY", in.IdentityID, true)
	case enum.EventUserDeleted, enum.EventUserUndeleted, enum.EventUserPurged, enum.EventUserFrozen, enum.EventUserUnfrozen:
		add("USER", in.UserID, true)
	case enum.EventSignInCompletionStarted, enum.EventSignInCompletionProofVerified, enum.EventSignInCompletionCompleted, enum.EventSignInCompletionRejected:
		add("IDENTITY_FLOW", in.FlowID, true)
	}
	// 在线 ADMIN_FORBIDDEN 不猜请求目标；历史入口保留旧行已保存的关联。
	if in.Type != enum.EventAdminForbidden || legacyID != "" {
		add("USER", in.UserID, false)
		add("SESSION", in.SessionID, false)
		add("IDENTITY_FLOW", in.FlowID, false)
		if in.Type == enum.EventSignInCompletionProofVerified && in.VerifiedSubject {
			add("IDENTITY", in.IdentityID, false)
		}
	}
	snap := snapshot{ActorKind: in.Actor.String(), FlowID: in.FlowID, SubjectHint: in.SubjectHint, LegacyEventID: legacyID}
	if legacyID != "" && in.Actor == enum.ActorAdmin && (in.AdminIssuer == "" || in.AdminSubject == "") {
		snap.AdminIssuer = in.AdminIssuer
		snap.AdminSubject = in.AdminSubject
	}
	if in.IdentityKind.Valid() {
		snap.IdentityKind = in.IdentityKind.String()
	}
	if !directSession {
		snap.SessionID = in.SessionID
	}
	wire, err := json.Marshal(data{Reason: in.Reason, Accountkit: snap})
	if err != nil {
		return event.Event{}, omitted, errors.New("audit/authentication: invalid data")
	}
	e.Data = wire
	e, err = event.Freeze(e)
	return e, omitted, err
}

func validActorKind(typ enum.EventType, kind enum.ActorKind) bool {
	switch typ {
	case enum.EventIdentityRevealed, enum.EventUserFrozen, enum.EventUserUnfrozen, enum.EventAdminForbidden:
		return kind == enum.ActorAdmin
	case enum.EventUserPurged:
		return kind == enum.ActorSystem
	case enum.EventSessionRevoked:
		return kind == enum.ActorUser || kind == enum.ActorAdmin || kind == enum.ActorSystem
	case enum.EventUserDeleted, enum.EventUserUndeleted:
		return kind == enum.ActorUser || kind == enum.ActorAdmin
	default:
		return kind == enum.ActorUser
	}
}

func actor(in Input, o Options) event.Actor {
	switch in.Actor {
	case enum.ActorAdmin:
		if in.AdminIssuer != "" && in.AdminSubject != "" {
			return event.Actor{Type: event.ActorUser, Namespace: in.AdminIssuer, ID: in.AdminSubject, Name: in.AdminUsername}
		}
		return event.Actor{Type: event.ActorUnknown, Name: in.AdminUsername}
	case enum.ActorSystem:
		return event.Actor{Type: event.ActorSystem, Namespace: o.Namespace, ID: "accountkit"}
	}
	anonymous := event.Actor{Type: event.ActorAnonymous}
	unknown := event.Actor{Type: event.ActorUnknown}
	switch in.Type {
	case enum.EventRefreshRejected, enum.EventRefreshReuseDetected:
		if in.UserID != "" || in.SessionID != "" {
			return unknown
		}
		return anonymous
	case enum.EventSignInCompletionStarted:
		return anonymous
	case enum.EventSignInCompletionRejected:
		if in.FlowID != "" || in.UserID != "" {
			return unknown
		}
		return anonymous
	case enum.EventSignInFailed, enum.EventSignInCompletionProofVerified:
		if !in.VerifiedSubject {
			return anonymous
		}
	case enum.EventIdentityReplaceRejected:
		if !in.VerifiedSubject {
			return unknown
		}
	}
	if in.UserID != "" {
		return event.Actor{Type: event.ActorUser, Namespace: o.Namespace, ID: in.UserID}
	}
	return anonymous
}

// Projection 只读取受控认证快照与明确资源；任意业务 data 不进入旧 HTTP DTO。
func Projection(e event.Event) (Input, error) {
	frozen, err := event.Freeze(e)
	if err != nil {
		return Input{}, err
	}
	e = frozen
	typ, err := enum.ParseEventType(e.Action)
	if err != nil {
		return Input{}, errors.New("audit/authentication: unknown action")
	}
	var d data
	if err := json.Unmarshal(e.Data, &d); err != nil {
		return Input{}, errors.New("audit/authentication: invalid projection")
	}
	var kind enum.ActorKind
	switch d.Accountkit.ActorKind {
	case "USER":
		kind = enum.ActorUser
	case "ADMIN":
		kind = enum.ActorAdmin
	case "SYSTEM":
		kind = enum.ActorSystem
	default:
		return Input{}, errors.New("audit/authentication: invalid actor_kind")
	}
	if d.Accountkit.LegacyEventID == "" && !validActorKind(typ, kind) {
		return Input{}, errors.New("audit/authentication: invalid actor for action")
	}
	in := Input{ID: e.ID, Type: typ, Actor: kind, Reason: d.Reason, SessionID: d.Accountkit.SessionID, FlowID: d.Accountkit.FlowID, SubjectHint: d.Accountkit.SubjectHint, IP: e.Context.ClientIP, DeviceID: e.Context.DeviceID, RequestID: e.Context.RequestID, OccurTime: e.OccurTime}
	if e.Result != nil {
		in.Result = *e.Result
	}
	if d.Accountkit.IdentityKind != "" {
		in.IdentityKind, err = enum.ParseIdentityKind(d.Accountkit.IdentityKind)
		if err != nil {
			return Input{}, errors.New("audit/authentication: invalid identity_kind")
		}
	}
	if !validText(in.SubjectHint, 8) {
		return Input{}, errors.New("audit/authentication: invalid subject_hint")
	}
	seenSession, seenFlow := false, false
	for _, r := range append(append([]event.Resource(nil), e.Targets...), e.Context.RelatedResources...) {
		switch r.Type {
		case "USER":
			if in.UserID != "" && in.UserID != r.ID {
				return Input{}, errors.New("audit/authentication: ambiguous user")
			}
			in.UserID = r.ID
		case "SESSION":
			if in.SessionID != "" && in.SessionID != r.ID {
				return Input{}, errors.New("audit/authentication: inconsistent session")
			}
			if in.SessionID == "" {
				in.SessionID = r.ID
			}
			seenSession = true
		case "IDENTITY":
			if in.IdentityID == "" {
				in.IdentityID = r.ID
			}
		case "IDENTITY_FLOW":
			if in.FlowID != "" && in.FlowID != r.ID {
				return Input{}, errors.New("audit/authentication: inconsistent flow")
			}
			if in.FlowID == "" {
				in.FlowID = r.ID
			}
			seenFlow = true
		}
	}
	if (in.SessionID != "" && !seenSession) || (in.FlowID != "" && !seenFlow) {
		return Input{}, errors.New("audit/authentication: missing resource reference")
	}
	if kind == enum.ActorAdmin {
		if e.Actor.Type != event.ActorUser && !(e.Actor.Type == event.ActorUnknown && d.Accountkit.LegacyEventID != "") {
			return Input{}, errors.New("audit/authentication: invalid administrator actor")
		}
		in.AdminIssuer = e.Actor.Namespace
		in.AdminSubject = e.Actor.ID
		in.AdminUsername = e.Actor.Name
		if e.Actor.Type == event.ActorUnknown && d.Accountkit.LegacyEventID != "" {
			if !validText(d.Accountkit.AdminIssuer, 512) || !validText(d.Accountkit.AdminSubject, 512) {
				return Input{}, errors.New("audit/authentication: invalid legacy administrator")
			}
			in.AdminIssuer = d.Accountkit.AdminIssuer
			in.AdminSubject = d.Accountkit.AdminSubject
		}
	}
	return in, nil
}

// Scrub 返回副本，保留稳定引用、追踪字段和非敏感 data。
func Scrub(e event.Event) event.Event {
	e = event.Scrub(e)
	var d map[string]json.RawMessage
	if json.Unmarshal(e.Data, &d) != nil {
		return e
	}
	var auth map[string]json.RawMessage
	if json.Unmarshal(d["accountkit"], &auth) != nil {
		return e
	}
	delete(auth, "subject_hint")
	d["accountkit"], _ = json.Marshal(auth)
	e.Data, _ = json.Marshal(d)
	return e
}

func validText(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// Stats 是自创建以来的安全观测；Omitted 计数被省略的可选字段。
type Stats struct{ Failed, Omitted uint64 }

// Recorder 为认证事件绑定来源配置，审计失败不返回到业务调用方。
type Recorder struct {
	out             event.Recorder
	options         Options
	logger          *slog.Logger
	newID           func() (uuid.UUID, error)
	failed, omitted atomic.Uint64
}

func NewRecorder(out event.Recorder, o Options, logger *slog.Logger) (*Recorder, error) {
	if out == nil {
		return nil, errors.New("audit/authentication: recorder required")
	}
	if err := o.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{out: out, options: o, logger: logger, newID: uuid.NewV7}, nil
}
func (r *Recorder) Record(ctx context.Context, in Input) {
	e, omitted, err := mapEvent(in, r.options, r.newID, "")
	for _, field := range omitted {
		r.omitted.Add(1)
		r.logger.WarnContext(ctx, "authentication audit optional field omitted", "field", field)
	}
	if err != nil {
		r.failed.Add(1)
		reason := "validation"
		if errors.Is(err, errIDGeneration) {
			reason = "id_generation"
		}
		r.logger.WarnContext(ctx, "authentication audit event rejected", "reason", reason)
		return
	}
	r.out.Record(ctx, e)
}
func (r *Recorder) Stats() Stats { return Stats{Failed: r.failed.Load(), Omitted: r.omitted.Load()} }
