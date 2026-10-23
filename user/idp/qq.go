package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

// QQOptions 注入 QQ 互联配置和可选宿主依赖；构造不进行 I/O。
type QQOptions struct {
	Config   QQConfig
	HTTP     *http.Client
	Logger   *slog.Logger
	Observer observability.Observer
}

// QQ 校验 QQ 互联移动 SDK 的 access_token。令牌不做一次性限制，每次请求都向 QQ 校验。
type QQ struct {
	config   parsedQQConfig
	http     *http.Client
	logger   *slog.Logger
	observer *observability.Recorder
}

// NewQQ 校验并复制配置；浅拷贝宿主 HTTP 客户端并禁用重定向。
func NewQQ(options QQOptions) (*QQ, error) {
	config, err := parseQQConfig(options.Config)
	if err != nil {
		return nil, qqFailure(ErrMisconfigured, "configuration")
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
	return &QQ{config: config, http: &isolated, logger: logger, observer: observability.NewRecorder(options.Observer, logger)}, nil
}

type qqSDKCredential struct {
	AppID       string `json:"app_id"`
	AccessToken string `json:"access_token"`
}

func (*QQ) Kind() enum.IdentityKind { return enum.IdentityQQ }

// Verify 实现 Provider：credential 为 {"app_id","access_token"}。
func (q *QQ) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	var c qqSDKCredential
	if err := decodeCredential(credential, &c); err != nil {
		return Identity{}, qqFailure(ErrMalformedCredential, "credential structure")
	}
	if c.AppID == "" || c.AccessToken == "" {
		return Identity{}, qqFailure(ErrMalformedCredential, "credential fields")
	}
	if !q.config.apps[c.AppID] {
		return Identity{}, qqFailure(ErrAppNotAllowed, "application not allowed")
	}
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	query := url.Values{"access_token": {c.AccessToken}, "fmt": {"json"}}
	if q.config.subjectMode == QQSubjectModeUnionID {
		// 未开通 UnionID 时上游整次返回 100048，OPENID 部署不能因此失败。
		query.Set("unionid", "1")
	}
	me, err := q.get(ctx, "/oauth2.0/me", query, 10*time.Second)
	if err != nil {
		return Identity{}, err
	}
	if raw, failed := me["error"]; failed {
		code, err := strconv.ParseInt(string(raw), 10, 64)
		if err != nil {
			return Identity{}, qqFailure(ErrUnavailable, "error structure")
		}
		return Identity{}, qqMeError(code)
	}
	client, clientErr := alipayString(me, "client_id")
	open, openErr := alipayString(me, "openid")
	union, unionErr := alipayString(me, "unionid")
	if clientErr != nil || openErr != nil || unionErr != nil || client == "" || !validAlipayIdentifier(open) || union != "" && !validAlipayIdentifier(union) {
		return Identity{}, qqFailure(ErrMisconfigured, "identity fields")
	}
	if client != c.AppID {
		return Identity{}, qqFailure(ErrAppNotAllowed, "token issued for another application")
	}
	subject := "openid:" + c.AppID + ":" + open
	if q.config.subjectMode == QQSubjectModeUnionID {
		if union == "" {
			return Identity{}, qqFailure(ErrMisconfigured, "missing unionid")
		}
		subject = "unionid:" + union
	}
	id := Identity{Kind: enum.IdentityQQ, Subject: subject, UnionID: union, OpenIDs: map[string]string{c.AppID: open}}
	name, picture, profileErr := q.profile(ctx, c.AppID, c.AccessToken, open)
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	if profileErr != nil {
		q.logger.WarnContext(ctx, "qq profile unavailable", "stage", "profile")
		q.observer.Observe(ctx, observability.DegradationObservation{Scenario: observability.DegradationQQProfileUnavailable, Count: 1})
	} else {
		id.Name, id.Picture = name, picture
	}
	return id, nil
}

// profile 在 Task 3 实现；此处保持无资料、无网络调用。
func (q *QQ) profile(ctx context.Context, appID, token, openID string) (string, string, error) {
	return "", "", nil
}
