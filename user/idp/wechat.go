package idp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
)

// App 是一个微信应用的凭据。
type App struct {
	AppID  string
	Secret string
}

// WeChatSubjectMode 是整个实例固定的微信主标识模式。
type WeChatSubjectMode string

const (
	// WeChatSubjectModeUnionID 使用 unionid:<unionid>，缺少 UnionID 时拒绝认证。
	WeChatSubjectModeUnionID WeChatSubjectMode = "UNIONID"
	// WeChatSubjectModeOpenID 使用 openid:<appid>:<openid>，不会自动升级到 UnionID。
	WeChatSubjectModeOpenID WeChatSubjectMode = "OPENID"
)

// WeChatConfig 配置 OAuth 和小程序应用；零值 SubjectMode 使用 UNIONID。
// 两组 AppID 不得重复，Secret 不参与策略指纹。
type WeChatConfig struct {
	Apps            []App
	MiniProgramApps []App
	SubjectMode     WeChatSubjectMode
	BaseURL         string
}

// WeChat 根据服务器应用配置交换 code，返回固定模式的主标识。
type WeChat struct {
	secrets      map[string]string
	miniPrograms map[string]bool
	mode         WeChatSubjectMode
	base         string
	hc           *http.Client
	logger       *slog.Logger
}

// NewWeChat 校验应用集合与主标识模式，不进行网络 I/O。
// 配置错误不回显配置值；空应用集合表示未启用。
func NewWeChat(config WeChatConfig, hc *http.Client, logger *slog.Logger) (*WeChat, error) {
	mode := config.SubjectMode
	if mode == "" {
		mode = WeChatSubjectModeUnionID
	}
	if mode != WeChatSubjectModeUnionID && mode != WeChatSubjectModeOpenID {
		return nil, fmt.Errorf("%w: invalid wechat subject mode", ErrMisconfigured)
	}
	m := make(map[string]string)
	mini := make(map[string]bool)
	for group, apps := range [][]App{config.Apps, config.MiniProgramApps} {
		for _, a := range apps {
			if strings.TrimSpace(a.AppID) == "" || strings.TrimSpace(a.Secret) == "" || strings.Contains(a.AppID, ":") {
				return nil, fmt.Errorf("%w: invalid wechat app credentials", ErrMisconfigured)
			}
			if _, ok := m[a.AppID]; ok {
				return nil, fmt.Errorf("%w: duplicate wechat app", ErrMisconfigured)
			}
			m[a.AppID] = a.Secret
			mini[a.AppID] = group == 1
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	// AppSecret 以查询参数形式带在请求 URL 里；跟随重定向会把它（连同 code）
	// 转发给重定向目标，因此在注入客户端的浅拷贝上禁用重定向跟随，把任何
	// 3xx 都当成非 200 处理（下面的状态码分支）。
	noRedirect := *hc
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &WeChat{secrets: m, miniPrograms: mini, mode: mode, base: strings.TrimRight(config.BaseURL, "/"), hc: &noRedirect, logger: logger}, nil
}

// tokenResponse 只解码验证所需的标识和错误码，不保留上游秘密或 errmsg。
type tokenResponse struct {
	OpenID  string `json:"openid"`
	UnionID string `json:"unionid"`
	ErrCode int    `json:"errcode"`
}

const maxWeChatBody = 64 << 10

// redactTransportErr unwraps every layer of *url.Error to its inner Err (which
// carries no URL), preventing the secret and code from leaking into logs or
// error messages. http.Client can wrap a *url.Error inside another *url.Error
// (e.g. redirect-checking failures), so this loops rather than unwrapping once.
func redactTransportErr(err error) error {
	for {
		var urlErr *url.Error
		if !errors.As(err, &urlErr) || urlErr.Err == nil {
			return err
		}
		err = urlErr.Err
	}
}

// VerifyWeChat 实现 WeChatVerifier。
type wechatCredential struct {
	AppID string `json:"app_id"`
	Code  string `json:"code"`
}

// Kind 实现 Provider。
func (w *WeChat) Kind() enum.IdentityKind { return enum.IdentityWeChat }

// Verify 实现 Provider：credential 为 {"app_id","code"}。
func (w *WeChat) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c wechatCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, err
	}
	if c.AppID == "" || c.Code == "" {
		return Identity{}, fmt.Errorf("%w: app_id and code are required", ErrMalformedCredential)
	}
	return w.VerifyWeChat(ctx, c.AppID, c.Code)
}

// VerifyWeChat 根据 appID 的服务器配置选择 OAuth 或小程序接口。
// 不获取额外资料；仅返回验证后的主标识、UnionID 与非空 OpenID。
func (w *WeChat) VerifyWeChat(ctx context.Context, appID, code string) (Identity, error) {
	secret, ok := w.secrets[appID]
	if !ok {
		return Identity{}, fmt.Errorf("%w: wechat app %q", ErrAppNotAllowed, appID)
	}
	if code == "" {
		return Identity{}, fmt.Errorf("%w: empty code", ErrInvalidCredential)
	}
	q := url.Values{"appid": {appID}, "secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
	path := "/sns/oauth2/access_token"
	if w.miniPrograms[appID] {
		path = "/sns/jscode2session"
		q.Del("code")
		q.Set("js_code", code)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: build request: %v", ErrUnavailable, redactTransportErr(err))
	}
	resp, err := w.hc.Do(req)
	if err != nil {
		w.logger.Warn("wechat token exchange failed", "app_id", appID, "err", redactTransportErr(err))
		return Identity{}, fmt.Errorf("%w: wechat request: %v", ErrUnavailable, redactTransportErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		w.logger.Warn("wechat token exchange non-200", "app_id", appID, "status", resp.StatusCode)
		return Identity{}, fmt.Errorf("%w: wechat http %d", ErrUnavailable, resp.StatusCode)
	}
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxWeChatBody)).Decode(&tr); err != nil {
		w.logger.Warn("wechat token exchange bad json", "app_id", appID)
		return Identity{}, fmt.Errorf("%w: invalid wechat response", ErrUnavailable)
	}
	switch tr.ErrCode {
	case 0:
	case 40029, 40163, 42003, 40226: // invalid code / code used / code expired；40226 高风险用户被微信拦截，属用户侧拒绝，重试无意义
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrInvalidCredential, tr.ErrCode)
	case 40013, 40125: // invalid appid / invalid secret
		w.logger.Error("wechat app credentials rejected", "app_id", appID, "errcode", tr.ErrCode)
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrMisconfigured, tr.ErrCode)
	default:
		w.logger.Warn("wechat token exchange error", "app_id", appID, "errcode", tr.ErrCode)
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrUnavailable, tr.ErrCode)
	}
	if w.mode == WeChatSubjectModeUnionID && tr.UnionID == "" {
		// §5.3：缺 unionid 是开放平台绑定问题，绝不降级用 openid。
		w.logger.Error("wechat app did not return unionid; bind the app to an open platform account", "app_id", appID)
		return Identity{}, fmt.Errorf("%w: wechat app %s returned no unionid", ErrMisconfigured, appID)
	}
	subject := "unionid:" + tr.UnionID
	if w.mode == WeChatSubjectModeOpenID {
		if tr.OpenID == "" {
			w.logger.Error("wechat app did not return openid", "app_id", appID)
			return Identity{}, fmt.Errorf("%w: wechat response missing openid", ErrMisconfigured)
		}
		subject = "openid:" + appID + ":" + tr.OpenID
	}
	id := Identity{Kind: enum.IdentityWeChat, Subject: subject, UnionID: tr.UnionID}
	if tr.OpenID != "" {
		id.OpenIDs = map[string]string{appID: tr.OpenID}
	}
	return id, nil
}
