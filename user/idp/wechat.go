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

// WeChat 用 code 换取 UnionID（服务端持有 AppSecret）。
type WeChat struct {
	secrets map[string]string
	base    string
	hc      *http.Client
	logger  *slog.Logger
}

// NewWeChat 构造微信校验器；apps 为空表示未启用。
func NewWeChat(apps []App, baseURL string, hc *http.Client, logger *slog.Logger) *WeChat {
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
	m := make(map[string]string, len(apps))
	for _, a := range apps {
		m[a.AppID] = a.Secret
	}
	return &WeChat{secrets: m, base: strings.TrimRight(baseURL, "/"), hc: &noRedirect, logger: logger}
}

// tokenResponse 是 sns/oauth2/access_token 的响应；access_token / refresh_token 只解码不使用。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	OpenID       string `json:"openid"`
	UnionID      string `json:"unionid"`
	ErrCode      int    `json:"errcode"`
	ErrMsg       string `json:"errmsg"`
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
func (w *WeChat) VerifyWeChat(ctx context.Context, appID, code string) (Identity, error) {
	secret, ok := w.secrets[appID]
	if !ok {
		return Identity{}, fmt.Errorf("%w: wechat app %q", ErrAppNotAllowed, appID)
	}
	if code == "" {
		return Identity{}, fmt.Errorf("%w: empty code", ErrInvalidCredential)
	}
	q := url.Values{"appid": {appID}, "secret": {secret}, "code": {code}, "grant_type": {"authorization_code"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.base+"/sns/oauth2/access_token?"+q.Encode(), nil)
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
		w.logger.Warn("wechat token exchange bad json", "app_id", appID, "err", err)
		return Identity{}, fmt.Errorf("%w: wechat response: %v", ErrUnavailable, err)
	}
	switch tr.ErrCode {
	case 0:
	case 40029, 40163, 42003, 40226: // invalid code / code used / code expired；40226 高风险用户被微信拦截，属用户侧拒绝，重试无意义
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrInvalidCredential, tr.ErrCode)
	case 40013, 40125: // invalid appid / invalid secret
		w.logger.Error("wechat app credentials rejected", "app_id", appID, "errcode", tr.ErrCode, "errmsg", tr.ErrMsg)
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrMisconfigured, tr.ErrCode)
	default:
		w.logger.Warn("wechat token exchange error", "app_id", appID, "errcode", tr.ErrCode, "errmsg", tr.ErrMsg)
		return Identity{}, fmt.Errorf("%w: wechat errcode %d", ErrUnavailable, tr.ErrCode)
	}
	if tr.UnionID == "" {
		// §5.3：缺 unionid 是开放平台绑定问题，绝不降级用 openid。
		w.logger.Error("wechat app did not return unionid; bind the app to an open platform account", "app_id", appID)
		return Identity{}, fmt.Errorf("%w: wechat app %s returned no unionid", ErrMisconfigured, appID)
	}
	id := Identity{Kind: enum.IdentityWeChat, Subject: tr.UnionID}
	if tr.OpenID != "" {
		id.OpenIDs = map[string]string{appID: tr.OpenID}
	}
	return id, nil
}
