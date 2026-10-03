package user

import (
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/db"
)

// Admin 是执行管理动作的管理端主体，来自宿主的 OIDC verifier（Issuer + Subject 才唯一标识一个人）。
// 只用于审计与冻结快照；库不存管理员表。
type Admin struct {
	Issuer   string
	Subject  string
	Username string
}

// FreezeInfo 是账号的冻结快照；仅 FROZEN 状态非 nil。
type FreezeInfo struct {
	Time          time.Time
	Reason        string
	ActorSubject  string
	ActorUsername string
}

// AdminUser 是管理端用户资源的数据源（含 C 端不可见的时间戳与冻结信息；不含任何身份密文/摘要）。
type AdminUser struct {
	ID          string
	State       enum.UserState
	DisplayName string
	CreateTime  time.Time
	UpdateTime  time.Time
	DeleteTime  *time.Time
	PurgeTime   *time.Time
	Freeze      *FreezeInfo
}

// AdminUserDetail 是用户详情：账号 + 掩码身份 + 活跃会话数。
type AdminUserDetail struct {
	AdminUser
	Identities         []IdentityInfo
	ActiveSessionCount int
}

// UserFilter 是管理端用户列表的过滤条件（由 HTTP 层从 AIP-160 filter 解析而来）。零值表示不过滤。
// 身份条件（IdentityKind/Subject/HintPrefix/HintSuffix）落在同一条活跃身份行上。
type UserFilter struct {
	State          *enum.UserState
	CreateTimeMin  *time.Time // 含
	CreateTimeMax  *time.Time // 不含
	IdentityKind   enum.IdentityKind
	Subject        *string // identity.phone / identity.email 的原始值；ListUsers 按 IdentityKind 归一化并展开为全部密钥版本的摘要
	HintPrefix     *string
	HintSuffix     *string
	IncludeDeleted bool // AIP-164 show_deleted：为 false 时排除 PENDING_DELETION 与 DELETED
}

// PageCursor 是 keyset 游标：上一页最后一行的 (排序时间, id)。
type PageCursor struct {
	Time time.Time
	ID   string
}

// UserPage 是一页用户列表；NextPageToken 由 HTTP 层编码，这里只给出游标。
type UserPage struct {
	Users      []AdminUser
	NextCursor *PageCursor // nil 表示没有下一页
}

// RevealedIdentity 是 :reveal 的结果：锚点身份为解密后的明文，第三方身份为 provider_subject。
type RevealedIdentity struct {
	ID      string
	Kind    enum.IdentityKind
	Subject string
}

func adminUserFrom(u db.UserAccount) AdminUser {
	out := AdminUser{ID: u.ID, State: u.State, CreateTime: u.CreateTime, UpdateTime: u.UpdateTime, DeleteTime: u.DeleteTime, PurgeTime: u.PurgeTime}
	if u.DisplayName != nil {
		out.DisplayName = *u.DisplayName
	}
	if u.State == enum.UserFrozen && u.FreezeTime != nil {
		f := &FreezeInfo{Time: *u.FreezeTime}
		if u.FreezeReason != nil {
			f.Reason = *u.FreezeReason
		}
		if u.FreezeActorSubject != nil {
			f.ActorSubject = *u.FreezeActorSubject
		}
		if u.FreezeActorUsername != nil {
			f.ActorUsername = *u.FreezeActorUsername
		}
		out.Freeze = f
	}
	return out
}
