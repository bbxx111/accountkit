package admin

import (
	"errors"
	"net/http"
	"strings"
	"time"

	auditdb "github.com/bbxx111/accountkit/audit/db"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/reqid"
	"github.com/bbxx111/accountkit/user"
)

// 管理端时间戳统一 RFC 3339 带纳秒（UTC）：审计事件与游标都需要亚秒精度。
func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func fmtTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := fmtTime(*t)
	return &s
}

// freezeResource 是冻结快照（用户资源的 freeze 字段；非 FROZEN 时为 null）。
type freezeResource struct {
	FreezeTime    string `json:"freeze_time"`
	Reason        string `json:"reason"`
	ActorSubject  string `json:"actor_subject"`
	ActorUsername string `json:"actor_username"`
}

// userResource 是管理端用户资源（与 C 端 meResource 分开定义：多出 update_time 与 freeze）。
type userResource struct {
	Name        string          `json:"name"`
	State       string          `json:"state"`
	DisplayName string          `json:"display_name"`
	CreateTime  string          `json:"create_time"`
	UpdateTime  string          `json:"update_time"`
	DeleteTime  *string         `json:"delete_time"`
	PurgeTime   *string         `json:"purge_time"`
	Freeze      *freezeResource `json:"freeze"`
}

func newUserResource(u user.AdminUser) userResource {
	out := userResource{
		Name: "users/" + u.ID, State: u.State.String(), DisplayName: u.DisplayName,
		CreateTime: fmtTime(u.CreateTime), UpdateTime: fmtTime(u.UpdateTime),
		DeleteTime: fmtTimePtr(u.DeleteTime), PurgeTime: fmtTimePtr(u.PurgeTime),
	}
	if u.Freeze != nil {
		out.Freeze = &freezeResource{FreezeTime: fmtTime(u.Freeze.Time), Reason: u.Freeze.Reason, ActorSubject: u.Freeze.ActorSubject, ActorUsername: u.Freeze.ActorUsername}
	}
	return out
}

type identityResource struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	MaskedSubject string `json:"masked_subject"`
	CreateTime    string `json:"create_time"`
}

// userDetailResource = 用户资源 + 掩码身份 + 活跃会话数。
type userDetailResource struct {
	userResource
	Identities         []identityResource `json:"identities"`
	ActiveSessionCount int                `json:"active_session_count"`
}

func newUserDetailResource(d user.AdminUserDetail) userDetailResource {
	out := userDetailResource{userResource: newUserResource(d.AdminUser), Identities: make([]identityResource, 0, len(d.Identities)), ActiveSessionCount: d.ActiveSessionCount}
	for _, i := range d.Identities {
		out.Identities = append(out.Identities, identityResource{Name: "users/" + d.ID + "/identities/" + i.ID, Kind: i.Kind.String(), MaskedSubject: i.MaskedSubject, CreateTime: fmtTime(i.CreateTime)})
	}
	return out
}

type userList struct {
	Users         []userResource `json:"users"`
	NextPageToken string         `json:"next_page_token"`
}

type revealResource struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
}

type sessionResource struct {
	Name         string `json:"name"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	CreateTime   string `json:"create_time"`
	LastUsedTime string `json:"last_used_time"`
}

type sessionList struct {
	Sessions []sessionResource `json:"sessions"`
}

type revokeAllResponse struct {
	RevokedCount int `json:"revoked_count"`
}

// auditEventResource 是审计事件的管理端 DTO；可空列输出 null。
type auditEventResource struct {
	Name          string  `json:"name"`
	EventType     string  `json:"event_type"`
	ActorKind     string  `json:"actor_kind"`
	Result        string  `json:"result"`
	Reason        *string `json:"reason"`
	SessionID     *string `json:"session_id"`
	IdentityKind  *string `json:"identity_kind"`
	SubjectHint   *string `json:"subject_hint"`
	IP            *string `json:"ip"`
	DeviceID      *string `json:"device_id"`
	RequestID     *string `json:"request_id"`
	AdminIssuer   *string `json:"admin_issuer"`
	AdminSubject  *string `json:"admin_subject"`
	AdminUsername *string `json:"admin_username"`
	OccurTime     string  `json:"occur_time"`
}

func newAuditEventResource(userID string, e auditdb.AuditEvent) auditEventResource {
	out := auditEventResource{
		Name: "users/" + userID + "/auditEvents/" + e.ID, EventType: e.EventType.String(), ActorKind: e.ActorKind.String(), Result: e.Result.String(),
		Reason: e.Reason, SessionID: e.SessionID, SubjectHint: e.SubjectHint, DeviceID: e.DeviceID, RequestID: e.RequestID,
		AdminIssuer: e.AdminIssuer, AdminSubject: e.AdminSubject, AdminUsername: e.AdminUsername, OccurTime: fmtTime(e.OccurTime),
	}
	if e.IdentityKind != nil && *e.IdentityKind != enum.IdentityKindUnspecified {
		k := e.IdentityKind.String()
		out.IdentityKind = &k
	}
	if e.Ip != nil {
		ip := e.Ip.String()
		out.IP = &ip
	}
	return out
}

type auditEventList struct {
	AuditEvents   []auditEventResource `json:"audit_events"`
	NextPageToken string               `json:"next_page_token"`
}

// writeServiceError 把领域错误映射为 AIP-193 响应。管理面上 403 只属于 verifier（角色不足）；
// 目标账号的状态问题一律 400 FAILED_PRECONDITION。
func (h *Handler) writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, user.ErrNotFound):
		apierror.Write(w, apierror.New(apierror.StatusNotFound, "NOT_FOUND", "resource not found"))
	case errors.Is(err, user.ErrInvalidState):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "INVALID_ACCOUNT_STATE", "operation not allowed in the account's current state"))
	case errors.Is(err, user.ErrUserFrozen):
		apierror.Write(w, apierror.New(apierror.StatusFailedPrecondition, "USER_FROZEN", "the account is frozen; unfreeze it first"))
	case errors.Is(err, user.ErrInvalidTarget):
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_FILTER", "invalid filter: identity.phone / identity.email value is not a valid phone number or email address"))
	case errors.Is(err, user.ErrInvalidArgument):
		msg := strings.TrimPrefix(strings.TrimPrefix(err.Error(), user.ErrInvalidArgument.Error()), ": ")
		if msg == "" {
			msg = "invalid argument"
		}
		apierror.Write(w, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", msg))
	default:
		apierror.WriteInternal(w, h.d.Logger, reqid.From(r.Context()), err)
	}
}
