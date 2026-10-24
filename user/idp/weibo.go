package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

// WeiboOptions 注入微博开放平台配置和可选宿主依赖；构造不进行 I/O。
type WeiboOptions struct {
	Config   WeiboConfig
	HTTP     *http.Client
	Logger   *slog.Logger
	Observer observability.Observer
}

// Weibo 校验微博移动 SDK 的 access_token。令牌不做一次性限制，每次请求都向微博校验。
// 微博 uid 全局唯一且公开，主标识不含 AppKey。
type Weibo struct {
	config   parsedWeiboConfig
	http     *http.Client
	logger   *slog.Logger
	observer *observability.Recorder
}

// NewWeibo 校验并复制配置；浅拷贝宿主 HTTP 客户端并禁用重定向。
func NewWeibo(options WeiboOptions) (*Weibo, error) {
	config, err := parseWeiboConfig(options.Config)
	if err != nil {
		return nil, weiboFailure(ErrMisconfigured, "configuration")
	}
	client := options.HTTP
	if client == nil {
		client = &http.Client{}
	}
	isolated := *client
	isolated.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Weibo{config: config, http: &isolated, logger: logger, observer: observability.NewRecorder(options.Observer, logger)}, nil
}

type weiboSDKCredential struct {
	AppID       string `json:"app_id"`
	AccessToken string `json:"access_token"`
}

func (*Weibo) Kind() enum.IdentityKind { return enum.IdentityWeibo }

// Verify 实现 Provider：credential 为 {"app_id","access_token"}，app_id 即微博 AppKey。
func (p *Weibo) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c weiboSDKCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, weiboFailure(ErrMalformedCredential, "credential structure")
	}
	if c.AppID == "" || c.AccessToken == "" {
		return Identity{}, weiboFailure(ErrMalformedCredential, "credential fields")
	}
	if !p.config.apps[c.AppID] {
		return Identity{}, weiboFailure(ErrAppNotAllowed, "application not allowed")
	}
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	status, info, err := p.do(ctx, http.MethodPost, "/oauth2/get_token_info", url.Values{"access_token": {c.AccessToken}}, 10*time.Second)
	if err != nil {
		return Identity{}, err
	}
	if _, failed := info["error_code"]; failed || status != http.StatusOK {
		code, err := weiboDecimal(info, "error_code")
		if err != nil || code == "" {
			return Identity{}, weiboFailure(ErrUnavailable, "error structure")
		}
		return Identity{}, weiboTokenError(code)
	}
	appKey, appErr := weiboDecimal(info, "appkey")
	if appErr != nil || appKey == "" {
		return Identity{}, weiboFailure(ErrMisconfigured, "identity fields")
	}
	if appKey != c.AppID {
		return Identity{}, weiboFailure(ErrAppNotAllowed, "token issued for another application")
	}
	expireText, expireErr := weiboDecimal(info, "expire_in")
	if expireErr != nil || expireText == "" {
		return Identity{}, weiboFailure(ErrMisconfigured, "identity fields")
	}
	if strings.HasPrefix(expireText, "-") {
		return Identity{}, weiboFailure(ErrInvalidCredential, "access token expired")
	}
	uid, uidErr := weiboDecimal(info, "uid")
	if uidErr != nil || uid == "" || uid == "0" || strings.HasPrefix(uid, "-") {
		return Identity{}, weiboFailure(ErrMisconfigured, "identity fields")
	}
	id := Identity{Kind: enum.IdentityWeibo, Subject: "uid:" + uid}
	// 剩余期限为 0 或超出 int64 / time.Duration 可表示范围时按未知处理：
	// 前者会让 pending 补全立即失效，后者会溢出成过去的截止时间。
	if expireIn, err := strconv.ParseInt(expireText, 10, 64); err == nil && expireIn > 0 && expireIn <= int64(math.MaxInt64/time.Second) {
		id.ExpireTime = time.Now().Add(time.Duration(expireIn) * time.Second)
	}
	name, picture, profileErr := p.profile(ctx, c.AccessToken, uid)
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	if profileErr != nil {
		p.logger.WarnContext(ctx, "weibo profile unavailable", "stage", "profile")
		p.observer.Observe(ctx, observability.DegradationObservation{Scenario: observability.DegradationWeiboProfileUnavailable, Count: 1})
	} else {
		id.Name, id.Picture = name, picture
	}
	return id, nil
}

// weiboAvatarURL 只保留 HTTPS 地址，不做协议改写；长度、字符与主机格式仍由领域层既有净化负责。
func weiboAvatarURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host == "" {
		return ""
	}
	return raw
}

// profile 使用本次令牌与已验证 uid 查询资料；任何失败都只返回固定阶段错误，由 Verify 记为降级。
func (p *Weibo) profile(ctx context.Context, token, uid string) (string, string, error) {
	status, object, err := p.do(ctx, http.MethodGet, "/2/users/show.json", url.Values{"access_token": {token}, "uid": {uid}}, 3*time.Second)
	if err != nil {
		return "", "", err
	}
	if _, failed := object["error_code"]; failed || status != http.StatusOK {
		return "", "", weiboFailure(ErrUnavailable, "profile result")
	}
	id, idErr := weiboDecimal(object, "id")
	idStr, idStrErr := weiboDecimal(object, "idstr")
	if idErr != nil || idStrErr != nil || id == "" && idStr == "" || id != "" && id != uid || idStr != "" && idStr != uid {
		return "", "", weiboFailure(ErrUnavailable, "profile user")
	}
	name, nameErr := alipayString(object, "screen_name")
	hd, hdErr := alipayString(object, "avatar_hd")
	large, largeErr := alipayString(object, "avatar_large")
	small, smallErr := alipayString(object, "profile_image_url")
	if nameErr != nil || hdErr != nil || largeErr != nil || smallErr != nil {
		return "", "", weiboFailure(ErrUnavailable, "profile fields")
	}
	picture := ""
	for _, candidate := range []string{hd, large, small} {
		if strings.TrimSpace(candidate) != "" {
			picture = candidate
			break
		}
	}
	return name, weiboAvatarURL(picture), nil
}
