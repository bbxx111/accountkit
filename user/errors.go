package user

import "errors"

var (
	// ErrDeletionBlocked：宿主业务检查拒绝注销 → 400 FAILED_PRECONDITION。
	ErrDeletionBlocked = errors.New("user: deletion blocked")
	// ErrInsufficientScope：调用者未获得完整账号操作作用域。
	ErrInsufficientScope = errors.New("user: insufficient scope")
	// ErrReauthenticationRequired：敏感操作要求近期认证。
	ErrReauthenticationRequired = errors.New("user: recent authentication required")
	// ErrIdentityUnchanged：新目标与当前身份相同。
	ErrIdentityUnchanged = errors.New("user: identity unchanged")
	// ErrUnavailable：Redis 等依赖不可用，调用方按 fail-closed 返回 503。
	ErrUnavailable = errors.New("user: dependency unavailable")
	// ErrInvalidTarget：手机号/邮箱无法归一化。
	ErrInvalidTarget = errors.New("user: invalid target")
	// ErrInvalidArgument：参数不合法（设备 id 缺失/超长、渠道不可用等）。
	ErrInvalidArgument = errors.New("user: invalid argument")
	// ErrInvalidGrant：refresh token 被拒（RFC 6749 invalid_grant）。
	ErrInvalidGrant = errors.New("user: invalid grant")
	// ErrInvalidToken：access token 无效或会话已吊销。
	ErrInvalidToken = errors.New("user: invalid access token")
	// ErrUserFrozen：账号被管理端冻结。
	ErrUserFrozen = errors.New("user: user is frozen")
	// ErrNotFound：资源不存在或不属于调用者。
	ErrNotFound = errors.New("user: not found")
	// ErrNotAnchor：target 不是该用户已绑定的锚点身份。
	ErrNotAnchor = errors.New("user: target is not an anchor identity of this user")
	// ErrIdentityConflict：该身份已被另一账号绑定 → 409 ALREADY_EXISTS。
	ErrIdentityConflict = errors.New("user: identity already bound to another account")
	// ErrIdentityKindLimit：同类身份数量已达上限 → 409 ALREADY_EXISTS。
	ErrIdentityKindLimit = errors.New("user: identity kind limit reached")
	// ErrLastAnchor：不能解绑该用户最后一个锚点身份 → 400 FAILED_PRECONDITION。
	ErrLastAnchor = errors.New("user: cannot unbind the last anchor identity")
	// ErrInvalidState：账号当前状态不允许该状态迁移（§1.1 迁移表之外的组合）→ 400 FAILED_PRECONDITION。
	ErrInvalidState = errors.New("user: operation not allowed in the account's current state")
	// ErrUserPendingDeletion：冷静期账号只接受锚点身份登录（§1.1）→ 403 PERMISSION_DENIED。
	ErrUserPendingDeletion = errors.New("user: user is pending deletion")
	// ErrUnknownKeyVersion：identity 活跃行使用了配置中不存在的 HMAC/加密密钥版本——旧密钥在回填完成前被移除。
	// Migrate 以此失败，阻止服务带着无法查找/解密的行启动。
	ErrUnknownKeyVersion = errors.New("user: identity rows use a key version that is not configured")
)
