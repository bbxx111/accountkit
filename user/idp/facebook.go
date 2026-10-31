package idp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

const (
	facebookIssuer       = "https://www.facebook.com"
	facebookJWKSDefault  = "https://www.facebook.com/.well-known/oauth/openid/jwks/"
	facebookGraphDefault = "https://graph.facebook.com"
	// FacebookGraphAPIVersionDefault 是固定的 Graph API 版本；升级属于兼容变更，须写入发布说明。
	FacebookGraphAPIVersionDefault = "v26.0"
	maxFacebookBody                = 64 << 10
)

// FacebookOptions 是 Facebook 校验器的依赖。AppSecret 只用于服务端 Graph 调用与 signed_request 校验。
type FacebookOptions struct {
	AppID, AppSecret, GraphAPIVersion, GraphAPIBaseURL, JWKSURL string
	HTTP                                                        *http.Client
	Nonces                                                      *NonceRegistry
	Logger                                                      *slog.Logger
	Observer                                                    observability.Observer
	Now                                                         func() time.Time
	// NonceTTL 是 Limited Login nonce 登记下限；实际期限覆盖 ID token 剩余有效期。默认 10m。
	NonceTTL time.Duration
}

// Facebook 校验 Limited Login 的 ID token 与 Classic Login 的 access token；两者的 subject
// 都是同一 App 下的用户 id，因此落到同一身份。Classic token 不做一次性限制（见 design）。
type Facebook struct {
	appID, secret, version, graph, jwksURL string
	hc                                     *http.Client
	logger                                 *slog.Logger
	now                                    func() time.Time
	v                                      *idTokenVerifier
}

// NewFacebook 构造校验器。不做 I/O；JWKS 首次 Limited 验证时加载。
// 构造不承诺完整启动校验；缺少验证依赖等配置问题可能在 Verify 时返回 ErrMisconfigured。
func NewFacebook(o FacebookOptions) *Facebook {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.NonceTTL <= 0 {
		o.NonceTTL = 10 * time.Minute
	}
	if o.GraphAPIVersion == "" {
		o.GraphAPIVersion = FacebookGraphAPIVersionDefault
	}
	if o.GraphAPIBaseURL == "" {
		o.GraphAPIBaseURL = facebookGraphDefault
	}
	if o.JWKSURL == "" {
		o.JWKSURL = facebookJWKSDefault
	}
	// token 与 appsecret_proof 在 query 中；跟随重定向会把它们转发给重定向目标。
	noRedirect := *o.HTTP
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	recorder := observability.NewRecorder(o.Observer, o.Logger)
	var clientIDs []string
	if o.AppID != "" {
		clientIDs = []string{o.AppID}
	}
	return &Facebook{
		appID: o.AppID, secret: o.AppSecret, version: o.GraphAPIVersion,
		graph: strings.TrimRight(o.GraphAPIBaseURL, "/"), jwksURL: o.JWKSURL,
		hc: &noRedirect, logger: o.Logger, now: o.Now,
		v: &idTokenVerifier{
			kind: enum.IdentityFacebook, label: "facebook", issuers: []string{facebookIssuer},
			clientIDs: clientIDSet(clientIDs),
			jwks:      newJWKSCache("facebook", o.JWKSURL, o.HTTP, o.Logger, recorder, observability.DegradationFacebookCachedKeyFallback, o.Now),
			nonces:    o.Nonces, nonceScope: "facebook", nonceTTL: o.NonceTTL, now: o.Now,
			nameClaims: []string{"name"}, pictureClaims: []string{"picture"}, hintEmailUnverified: true,
		},
	}
}

// Kind 实现 Provider。
func (f *Facebook) Kind() enum.IdentityKind { return enum.IdentityFacebook }

// facebookCredential 用指针区分“缺失”与“空字符串”，二者都按形状错误处理。
type facebookCredential struct {
	IDToken     *string `json:"id_token"`
	Nonce       *string `json:"nonce"`
	AccessToken *string `json:"access_token"`
}

// Verify 实现 Provider：credential 为 {"id_token","nonce"} 或 {"access_token"}，二者择一。
func (f *Facebook) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c facebookCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, err
	}
	switch {
	case c.AccessToken == nil && c.IDToken != nil && c.Nonce != nil && *c.IDToken != "" && *c.Nonce != "":
		return f.VerifyLimited(ctx, *c.IDToken, *c.Nonce)
	case c.AccessToken != nil && c.IDToken == nil && c.Nonce == nil && *c.AccessToken != "":
		return f.VerifyClassic(ctx, *c.AccessToken)
	}
	return Identity{}, fmt.Errorf("%w: facebook credential must be {id_token, nonce} or {access_token}", ErrMalformedCredential)
}

// VerifyLimited 校验 Limited Login ID token 与原始 nonce。
func (f *Facebook) VerifyLimited(ctx context.Context, idToken, rawNonce string) (Identity, error) {
	return f.v.verify(ctx, idToken, rawNonce)
}

type graphError struct {
	Type string `json:"type"`
	Code int    `json:"code"`
}

// throttled 是 Graph 的限流错误码，属于依赖暂不可用而非凭证或配置问题。
func (e *graphError) throttled() bool {
	switch e.Code {
	case 4, 17, 32, 613:
		return true
	}
	return false
}

type debugTokenResponse struct {
	Data struct {
		AppID     string `json:"app_id"`
		Type      string `json:"type"`
		IsValid   bool   `json:"is_valid"`
		UserID    string `json:"user_id"`
		ExpiresAt int64  `json:"expires_at"`
	} `json:"data"`
	Error *graphError `json:"error"`
}

type meResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Picture struct {
		Data struct {
			URL          string `json:"url"`
			IsSilhouette bool   `json:"is_silhouette"`
		} `json:"data"`
	} `json:"picture"`
	Error *graphError `json:"error"`
}

func (f *Facebook) proof(token string) string {
	m := hmac.New(sha256.New, []byte(f.secret))
	m.Write([]byte(token))
	return hex.EncodeToString(m.Sum(nil))
}

// graphGet 发起 GET 并把 JSON 解到 dst。4xx 的错误体同样解码交给调用方判断；5xx、传输与解码错误为不可用。
// 错误与日志不含 URL（其中有 token 与 proof）。
func (f *Facebook) graphGet(ctx context.Context, path string, q url.Values, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.graph+"/"+f.version+path+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("%w: facebook build request: %v", ErrUnavailable, redactTransportErr(err))
	}
	resp, err := f.hc.Do(req)
	if err != nil {
		f.logger.Warn("facebook graph request failed", "path", path, "err", redactTransportErr(err))
		return fmt.Errorf("%w: facebook request: %v", ErrUnavailable, redactTransportErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 || (resp.StatusCode >= 300 && resp.StatusCode < 400) {
		f.logger.Warn("facebook graph non-success", "path", path, "status", resp.StatusCode)
		return fmt.Errorf("%w: facebook http %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFacebookBody))
	if err != nil {
		return fmt.Errorf("%w: facebook response: %v", ErrUnavailable, redactTransportErr(err))
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: facebook response: %v", ErrUnavailable, err)
	}
	if resp.StatusCode >= 400 {
		// 4xx 必须带 Graph 错误对象；否则不能当作成功响应。
		var probe struct {
			Error *graphError `json:"error"`
		}
		if json.Unmarshal(body, &probe) != nil || probe.Error == nil {
			f.logger.Warn("facebook graph 4xx without error object", "path", path, "status", resp.StatusCode)
			return fmt.Errorf("%w: facebook http %d", ErrUnavailable, resp.StatusCode)
		}
	}
	return nil
}

// VerifyClassic 用 debug_token 确认 token 属于本 App 的用户，再用 /me 读取资料并核对 id。
func (f *Facebook) VerifyClassic(ctx context.Context, accessToken string) (Identity, error) {
	if f.appID == "" {
		return Identity{}, fmt.Errorf("%w: facebook sign-in not configured", ErrAppNotAllowed)
	}
	appToken := f.appID + "|" + f.secret
	var dt debugTokenResponse
	if err := f.graphGet(ctx, "/debug_token", url.Values{"input_token": {accessToken}, "access_token": {appToken}, "appsecret_proof": {f.proof(appToken)}}, &dt); err != nil {
		return Identity{}, err
	}
	if dt.Error != nil {
		if dt.Error.throttled() {
			return Identity{}, fmt.Errorf("%w: facebook debug_token throttled", ErrUnavailable)
		}
		f.logger.Error("facebook app credentials rejected", "app_id", f.appID, "code", dt.Error.Code, "type", dt.Error.Type)
		return Identity{}, fmt.Errorf("%w: facebook debug_token code %d", ErrMisconfigured, dt.Error.Code)
	}
	d := dt.Data
	if d.AppID != "" && d.AppID != f.appID {
		return Identity{}, fmt.Errorf("%w: facebook token issued for another app", ErrAppNotAllowed)
	}
	if !d.IsValid || d.Type != "USER" || d.UserID == "" || d.AppID == "" {
		return Identity{}, fmt.Errorf("%w: facebook token invalid", ErrInvalidCredential)
	}
	var me meResponse
	if err := f.graphGet(ctx, "/me", url.Values{"fields": {"id,name,email,picture"}, "access_token": {accessToken}, "appsecret_proof": {f.proof(accessToken)}}, &me); err != nil {
		return Identity{}, err
	}
	if me.Error != nil {
		switch {
		case me.Error.throttled():
			return Identity{}, fmt.Errorf("%w: facebook me throttled", ErrUnavailable)
		case me.Error.Code == 190:
			return Identity{}, fmt.Errorf("%w: facebook token rejected", ErrInvalidCredential)
		}
		f.logger.Error("facebook profile request rejected", "app_id", f.appID, "code", me.Error.Code, "type", me.Error.Type)
		return Identity{}, fmt.Errorf("%w: facebook me code %d", ErrMisconfigured, me.Error.Code)
	}
	if me.ID != d.UserID {
		return Identity{}, fmt.Errorf("%w: facebook user id mismatch", ErrInvalidCredential)
	}
	id := Identity{Kind: enum.IdentityFacebook, Subject: d.UserID, Name: me.Name, HintEmail: me.Email}
	if !me.Picture.Data.IsSilhouette {
		id.Picture = me.Picture.Data.URL
	}
	if d.ExpiresAt > 0 {
		id.ExpireTime = time.Unix(d.ExpiresAt, 0).UTC()
	}
	return id, nil
}
