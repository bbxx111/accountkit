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

// CurrentSessionInfo 是当前会话的详情；来源是否已知与客户端描述是否缺失相互独立。
type CurrentSessionInfo struct {
	SessionInfo
	AuthTime                      time.Time
	AuthenticationIdentitiesKnown bool
	AuthenticationIdentities      []AuthenticationIdentity
	InitialClientInfo             *ClientInfo
}
