package gointegration_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/anonymize"
	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/jackc/pgx/v5"
)

// 使用宿主配置与连接池；New 本身不连接外部依赖。
func Example_lifecycle() {
	var cfg accountkit.Config // 宿主加载并校验的配置，包含密钥与身份策略。
	var deps accountkit.Deps  // 宿主创建的 Pool、Redis、发送器与业务回调。
	ctx := context.Background()
	auth, err := accountkit.New(cfg, deps)
	if err != nil {
		return // 宿主报告安全诊断并终止启动。
	}
	defer auth.Close() // 先于宿主的审计组件、Pool、Redis 关闭。
	startup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := auth.Migrate(startup); err != nil {
		return // 不接受业务请求，不 Force、不自动清库。
	}
	auth.Start(ctx) // 使用服务生命周期 context，不能使用请求或 startup context。
	// 此处进入宿主服务循环；返回前先停止接收请求并完成所有在途调用。
	// Shutdown 超时并不代表请求已排空，不能继续关闭在用的数据库连接。
}

// 发码与提交凭证通常属于两个请求；宿主将 CodeID/ExpireTime 返回给客户端。
func Example_signInWithCode() {
	var auth *accountkit.Auth      // 已装配、迁移并启动的宿主门面。
	var target, enteredCode string // 两次请求中相同的邮箱目标，以及用户输入的验证码。
	var device user.Device         // ID 来自客户端稳定设备标识，Name 可选。
	var meta user.Meta             // IP 来自可信解析，RequestID 由宿主生成/校验。
	ctx := context.Background()
	challenge, err := auth.Users().SendSignInCode(ctx, enum.IdentityEmail, target, meta)
	if err != nil {
		return
	}
	// 用户取得邮件后提交 target、challenge.CodeID、enteredCode；不要读取发送器内部验证码。
	result, err := auth.Users().SignInWithCode(ctx, user.CodeCredential{
		Channel: enum.IdentityEmail, Target: target, CodeID: challenge.CodeID, Code: enteredCode,
	}, device, meta)
	if err != nil {
		return
	}
	if result.Pending != nil {
		// 使用独立补全票据继续流程；此时尚无本地账号或 token pair。
		return
	}
	// 根据 result.Tokens.Scope 选择正常业务、身份绑定或恢复流程。
	// 通过宿主明确的响应 DTO 安全交付令牌，不直接 JSON 序列化领域结果或打印凭证。
	_ = result.Tokens
}

func Example_completeSignIn() {
	var auth *accountkit.Auth
	var pending *user.PendingSignIn // 上次 SignInWithCode/SignInWithIdp 返回的 Pending。
	var target, enteredCode string
	var meta user.Meta
	ctx := context.Background()
	// 后续设备字段可全部省略；若提供，必须与最初登录一致。
	challenge, err := auth.Users().SendSignInCompletionCode(ctx,
		pending.SignInCompletionToken, enum.IdentityEmail, target, user.Device{}, meta)
	if err != nil {
		return
	}
	// 另一次请求提交用户输入的验证码；界面说明验证会关联身份或自动注册。
	result, err := auth.Users().CompleteSignIn(ctx, pending.SignInCompletionToken,
		user.CodeCredential{Channel: enum.IdentityEmail, Target: target,
			CodeID: challenge.CodeID, Code: enteredCode}, user.Device{}, meta)
	if err != nil {
		return
	}
	if result.Pending != nil {
		return // 尚未满足身份策略，按返回的策略继续补全；原期限不延长。
	}
	// 返回 token 仍须按 Scope 分流，可能是既有不完整账号的 user:bind。
	// 分流方式见 Example_signInResult；不保留超过原期限的补全票据。
	_ = result.Tokens
}

// 这里只演示四种结果的分流；使用的对象是合成数据，不是真实登录。
func Example_signInResult() {
	next := func(result user.SignInResult) string {
		if (result.Tokens == nil) == (result.Pending == nil) {
			return "invalid result"
		}
		if result.Pending != nil {
			return "complete sign-in"
		}
		switch result.Tokens.Scope {
		case user.ScopeUser:
			return "continue"
		case user.ScopeBind:
			return "bind identity"
		case user.ScopeUndelete:
			return "restore account"
		default:
			return "unsupported scope"
		}
	}
	fmt.Println(next(user.SignInResult{Pending: &user.PendingSignIn{}}))
	for _, scope := range []string{user.ScopeUser, user.ScopeBind, user.ScopeUndelete} {
		fmt.Println(next(user.SignInResult{Tokens: &user.TokenResult{Scope: scope}}))
	}
	// Output:
	// complete sign-in
	// continue
	// bind identity
	// restore account
}

func Example_authenticate() {
	var auth *accountkit.Auth
	var rawAccess string // 宿主从请求获取的 access token，去掉 Bearer 前缀。
	ctx := context.Background()
	p, err := auth.Users().Authenticate(ctx, rawAccess)
	if err != nil {
		return
	}
	if p.Scope != user.ScopeUser {
		return // 返回宿主的作用域不足错误；不能让 user:bind/user:undelete 进入普通业务。
	}
	profile, err := auth.Users().GetMe(ctx, p.UserID)
	if err != nil {
		return
	}
	_ = profile
	// 自有资源查询须按 p.UserID 限定归属；不能相信请求体中的 user_id。
	// Authenticate 不查询数据库确认账号仍 ACTIVE；需要事务保护时用 WithActiveUsers。
}

func Example_refreshAndReauthenticate() {
	var auth *accountkit.Auth
	var current user.TokenResult // 安全存储的当前令牌对；同一会话的更新由宿主串行协调。
	var meta user.Meta
	var reauthCredential user.CodeCredential // SendReauthenticationCode 发出的 REAUTH 轮次。
	ctx := context.Background()
	rotated, err := auth.Users().Refresh(ctx, current.RefreshToken, meta)
	if err != nil {
		return
	}
	current = rotated // 在安全存储中原子替换 access 和 refresh，避免乱序响应覆盖。
	p, err := auth.Users().Authenticate(ctx, current.AccessToken)
	if err != nil {
		return
	}
	// REAUTH 的发码和校验需使用同一账号/会话；刷新不改变 SessionID。
	reauthenticated, err := auth.Users().Reauthenticate(ctx, p, reauthCredential, meta)
	if err != nil {
		return
	}
	// 重新认证只签发 access；不能用空 RefreshToken 覆盖已有 refresh。
	current.AccessToken = reauthenticated.AccessToken
	current.ExpiresIn = reauthenticated.ExpiresIn
	current.Scope = reauthenticated.Scope
	// 保留原 refresh 及其绝对到期时刻，不从此刻重算 RefreshExpiresIn。
	_ = current
}

// 业务 SQL 属于宿主；示例的回调类型明确要求使用库传入的同一事务。
func Example_withActiveUsers() {
	var auth *accountkit.Auth
	var authorizedUserIDs []string                            // 宿主已完成业务授权，不能直接接受任意客户端 ID 集合。
	var writeBusinessRows func(context.Context, pgx.Tx) error // 宿主自己的仓储实现。
	ctx := context.Background()
	if len(authorizedUserIDs) == 0 {
		return // 空业务批次直接跳过，不调用 WithActiveUsers。
	}
	err := auth.Users().WithActiveUsers(ctx, authorizedUserIDs, func(tx pgx.Tx) error {
		// 此时全部账号已按 ID 排序锁定并确认 ACTIVE；之后才取得业务资源锁。
		// 不使用 pool.Exec、不自行提交/回滚、不在此等待远端服务。
		return writeBusinessRows(ctx, tx)
	})
	if err != nil {
		return // 库已回滚；panic 也先回滚再向上传播。
	}
}

// 回调示例不创建或操作任何产品表；替换为宿主自己定义的业务查询。
func Example_beforeDelete() {
	var hasBlockingBusiness func(context.Context, pgx.Tx, string) (bool, error)
	beforeDelete := user.BeforeDelete(func(ctx context.Context, tx pgx.Tx, userID string) error {
		blocked, err := hasBlockingBusiness(ctx, tx, userID)
		if err != nil {
			return err // 基础设施错误保留供宿主安全诊断，不能伪装成允许注销。
		}
		if blocked {
			return user.ErrDeletionBlocked
		}
		return nil
	})
	_ = accountkit.Deps{BeforeDelete: beforeDelete}
}

// profileAnonymizer 代表宿主仓储；Tables 应列出宿主真实覆盖的所有表。
type profileAnonymizer struct {
	clearProfile func(context.Context, pgx.Tx, string) error
}

func (profileAnonymizer) Name() string     { return "host-profile" }
func (profileAnonymizer) Tables() []string { return []string{"profile"} }
func (a profileAnonymizer) Anonymize(ctx context.Context, tx pgx.Tx, userID string) error {
	return a.clearProfile(ctx, tx, userID)
}

var _ anonymize.Anonymizer = profileAnonymizer{}

func Example_anonymizer() {
	var clearProfile func(context.Context, pgx.Tx, string) error
	_ = accountkit.Deps{Anonymizers: []anonymize.Anonymizer{
		profileAnonymizer{clearProfile: clearProfile},
	}}
	// 同库、同事务；失败使认证数据和业务数据一起回滚。宿主须测试表覆盖性。
	// 外部副本通过宿主持久化清理任务处理，不能仅依赖 USER_PURGED 审计通知。
}

func Example_errors() {
	err := fmt.Errorf("sign-in rejected: %w", code.ErrInvalid)
	fmt.Println(errors.Is(err, code.ErrInvalid))
	err = fmt.Errorf("send rejected: %w", &code.RateLimitedError{
		Dimension: "COOLDOWN", RetryAfter: time.Minute,
	})
	var limited *code.RateLimitedError
	if errors.As(err, &limited) {
		fmt.Println(limited.Dimension, limited.RetryAfter)
	}
	// Output:
	// true
	// COOLDOWN 1m0s
}

// 具体name比较可区分同类型的不同身份；未知来源不能解释为安全。
func Example_currentSessionImpact() {
	affected := func(current user.CurrentSessionInfo, userID, targetName string) bool {
		if !current.AuthenticationIdentitiesKnown {
			return true
		}
		for _, identity := range current.AuthenticationIdentities {
			if "users/"+userID+"/identities/"+identity.ID == targetName {
				return true
			}
		}
		return false
	}
	const uid = "u_0000000000001"
	const target = "users/" + uid + "/identities/i_0000000000001"
	fmt.Println(affected(user.CurrentSessionInfo{}, uid, target))
	current := user.CurrentSessionInfo{AuthenticationIdentitiesKnown: true, AuthenticationIdentities: []user.AuthenticationIdentity{{ID: "i_0000000000002", Kind: enum.IdentityEmail}}}
	fmt.Println(affected(current, uid, target)) // 同为EMAIL，具体身份不同。
	current.AuthenticationIdentities = append(current.AuthenticationIdentities, user.AuthenticationIdentity{ID: "i_0000000000001", Kind: enum.IdentityEmail})
	fmt.Println(affected(current, uid, target))
	// Output:
	// true
	// false
	// true
}

func Example_unbindIdentity() {
	var auth *accountkit.Auth
	var current user.TokenResult
	var credential user.CodeCredential // 保留身份的REAUTH轮次，不是新目标的BIND证明。
	var targetIdentityID string
	var meta user.Meta
	var showExitWarning func() // 交互层展示确认提示；此片段假定已取得用户操作意愿。
	var clearCredentials func()
	ctx := context.Background()
	p, err := auth.Users().Authenticate(ctx, current.AccessToken)
	if err != nil {
		return
	}
	reauthenticated, err := auth.Users().Reauthenticate(ctx, p, credential, meta)
	if err != nil {
		return
	}
	current.AccessToken = reauthenticated.AccessToken // 保留原refresh与其绝对期限。
	current.ExpiresIn, current.Scope = reauthenticated.ExpiresIn, reauthenticated.Scope
	p, err = auth.Users().Authenticate(ctx, current.AccessToken)
	if err != nil || p.Scope != user.ScopeUser {
		return
	}
	details, err := auth.Users().GetCurrentSession(ctx, p) // 重新认证后必须重查。
	if err != nil {
		return
	}
	targetName := "users/" + p.UserID + "/identities/" + targetIdentityID
	mayExit := !details.AuthenticationIdentitiesKnown
	for _, identity := range details.AuthenticationIdentities {
		if "users/"+p.UserID+"/identities/"+identity.ID == targetName {
			mayExit = true
		}
	}
	if mayExit {
		showExitWarning()
	} // unknown也保守提示，不能只按Kind比较。
	if err := auth.Users().UnbindIdentity(ctx, p, targetIdentityID, meta); err != nil {
		if errors.Is(err, user.ErrInvalidToken) {
			clearCredentials()
		}
		return
	}
	// 对应HTTP 204，解绑已成功；查询预览不能承诺并发操作后的最终结果。
	if mayExit {
		clearCredentials()
		return
	}
	if _, err := auth.Users().GetCurrentSession(ctx, p); errors.Is(err, user.ErrInvalidToken) {
		clearCredentials() // 成功解绑后若会话实际失效，退出并重新登录，不重试解绑。
	}
	// 依赖不可用不冒充凭据失效，由宿主展示暂时不可用并按需重新查询。
}
