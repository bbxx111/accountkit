package user

import (
	"github.com/bbxx111/accountkit/enum"
	"time"
)

// AuthenticationIdentity 是会话当前认证依据中的身份摘要。
type AuthenticationIdentity struct {
	ID   string
	Kind enum.IdentityKind
}

// CurrentSessionInfo 是当前会话的详情；认证来源始终非空，客户端描述可缺失。
type CurrentSessionInfo struct {
	SessionInfo
	AuthTime                 time.Time
	AuthenticationIdentities []AuthenticationIdentity
}
