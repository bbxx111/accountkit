// Package enum 定义 auth-server 所有存于 SMALLINT 列的枚举（AGENTS.md：枚举在 Go 定义，
// DB 无 CHECK；值 append-only；零值 UNSPECIFIED 不存储不发出）。sqlc 通过 go_type 覆盖把列绑到这些类型。
//
// 数值已被 0001_init.up.sql 的注释与索引谓词钉死（如 purge 索引 WHERE state = 3），修改即破坏迁移。
package enum

import "fmt"

// ---- UserState ----

type UserState int16

const (
	UserStateUnspecified UserState = iota
	UserActive                     // 1
	UserFrozen                     // 2
	UserPendingDeletion            // 3
	UserDeleted                    // 4
)

var userStateNames = [...]string{"UNSPECIFIED", "ACTIVE", "FROZEN", "PENDING_DELETION", "DELETED"}

func (s UserState) String() string { return name(userStateNames[:], int(s)) }
func (s UserState) Valid() bool    { return s >= UserActive && s <= UserDeleted }

// ParseUserState 只接受精确的 UPPER_SNAKE 名（AIP-126）；UNSPECIFIED 与未知值均报错。
func ParseUserState(v string) (UserState, error) {
	i, err := parse(userStateNames[:], v, "UserState")
	return UserState(i), err
}

// ---- IdentityKind ----

type IdentityKind int16

const (
	IdentityKindUnspecified IdentityKind = iota
	IdentityPhone                        // 1
	IdentityEmail                        // 2
	IdentityWeChat                       // 3
	IdentityApple                        // 4
)

var identityKindNames = [...]string{"UNSPECIFIED", "PHONE", "EMAIL", "WECHAT", "APPLE"}

func (k IdentityKind) String() string { return name(identityKindNames[:], int(k)) }
func (k IdentityKind) Valid() bool    { return k >= IdentityPhone && k <= IdentityApple }

// IsAnchor 报告该 kind 是否为锚点身份（账号至少需一个）。
func (k IdentityKind) IsAnchor() bool { return k == IdentityPhone || k == IdentityEmail }

// IsChannel 报告该 kind 是否可作为验证码发送渠道。
func (k IdentityKind) IsChannel() bool { return k == IdentityPhone || k == IdentityEmail }

func ParseIdentityKind(v string) (IdentityKind, error) {
	i, err := parse(identityKindNames[:], v, "IdentityKind")
	return IdentityKind(i), err
}

// ---- RevokeReason ----

type RevokeReason int16

const (
	RevokeReasonUnspecified RevokeReason = iota
	RevokeUserLogout                     // 1
	RevokeUserRevokedDevice              // 2
	RevokeReuseDetected                  // 3
	RevokeUserFrozen                     // 4
	RevokeUserDeleted                    // 5
	RevokeReplacedByRelogin              // 6
	RevokeAdmin                          // 7
)

var revokeReasonNames = [...]string{"UNSPECIFIED", "USER_LOGOUT", "USER_REVOKED_DEVICE", "REUSE_DETECTED", "USER_FROZEN", "USER_DELETED", "REPLACED_BY_RELOGIN", "ADMIN"}

func (r RevokeReason) String() string { return name(revokeReasonNames[:], int(r)) }
func (r RevokeReason) Valid() bool    { return r >= RevokeUserLogout && r <= RevokeAdmin }

func ParseRevokeReason(v string) (RevokeReason, error) {
	i, err := parse(revokeReasonNames[:], v, "RevokeReason")
	return RevokeReason(i), err
}

// ---- CodePurpose ----

type CodePurpose int16

const (
	CodePurposeUnspecified CodePurpose = iota
	PurposeSignIn                      // 1
	PurposeBind                        // 2
	PurposeReauth                      // 3
)

var codePurposeNames = [...]string{"UNSPECIFIED", "SIGN_IN", "BIND", "REAUTH"}
var codePurposeKeys = [...]string{"unspecified", "signin", "bind", "reauth"}

func (p CodePurpose) String() string { return name(codePurposeNames[:], int(p)) }
func (p CodePurpose) Valid() bool    { return p >= PurposeSignIn && p <= PurposeReauth }

// Key 返回 Redis 键中使用的小写片段。
func (p CodePurpose) Key() string { return name(codePurposeKeys[:], int(p)) }

// ---- ActorKind / Result / EventType（审计） ----

type ActorKind int16

const (
	ActorKindUnspecified ActorKind = iota
	ActorUser                      // 1
	ActorAdmin                     // 2
	ActorSystem                    // 3
)

var actorKindNames = [...]string{"UNSPECIFIED", "USER", "ADMIN", "SYSTEM"}

func (a ActorKind) String() string { return name(actorKindNames[:], int(a)) }
func (a ActorKind) Valid() bool    { return a >= ActorUser && a <= ActorSystem }

type Result int16

const (
	ResultUnspecified Result = iota
	ResultSuccess            // 1
	ResultFailure            // 2
)

var resultNames = [...]string{"UNSPECIFIED", "SUCCESS", "FAILURE"}

func (r Result) String() string { return name(resultNames[:], int(r)) }
func (r Result) Valid() bool    { return r == ResultSuccess || r == ResultFailure }

type EventType int16

const (
	EventTypeUnspecified        EventType = iota
	EventCodeSent                         // 1
	EventCodeSendRejected                 // 2
	EventCodeSendFailed                   // 3
	EventSignIn                           // 4
	EventSignInFailed                     // 5
	EventReauthenticated                  // 6
	EventReauthenticationFailed           // 7
	EventTokenRefreshed                   // 8
	EventRefreshRejected                  // 9
	EventRefreshReuseDetected             // 10
	EventSessionRevoked                   // 11
	EventIdentityBound                    // 12
	EventIdentityBindRejected             // 13
	EventIdentityUnbound                  // 14
	EventIdentityRevealed                 // 15
	EventUserDeleted                      // 16
	EventUserUndeleted                    // 17
	EventUserPurged                       // 18
	EventUserFrozen                       // 19
	EventUserUnfrozen                     // 20
	EventAdminForbidden                   // 21
)

var eventTypeNames = [...]string{
	"UNSPECIFIED", "CODE_SENT", "CODE_SEND_REJECTED", "CODE_SEND_FAILED", "SIGN_IN", "SIGN_IN_FAILED",
	"REAUTHENTICATED", "REAUTHENTICATION_FAILED", "TOKEN_REFRESHED", "REFRESH_REJECTED", "REFRESH_REUSE_DETECTED",
	"SESSION_REVOKED", "IDENTITY_BOUND", "IDENTITY_BIND_REJECTED", "IDENTITY_UNBOUND", "IDENTITY_REVEALED",
	"USER_DELETED", "USER_UNDELETED", "USER_PURGED", "USER_FROZEN", "USER_UNFROZEN", "ADMIN_FORBIDDEN",
}

func (e EventType) String() string { return name(eventTypeNames[:], int(e)) }
func (e EventType) Valid() bool    { return e >= EventCodeSent && e <= EventAdminForbidden }

func ParseEventType(v string) (EventType, error) {
	i, err := parse(eventTypeNames[:], v, "EventType")
	return EventType(i), err
}

// ---- helpers ----

func name(names []string, i int) string {
	if i <= 0 || i >= len(names) {
		return names[0]
	}
	return names[i]
}

func parse(names []string, v, typ string) (int, error) {
	for i := 1; i < len(names); i++ {
		if names[i] == v {
			return i, nil
		}
	}
	return 0, fmt.Errorf("enum: invalid %s %q", typ, v)
}
