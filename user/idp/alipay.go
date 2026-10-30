package idp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/observability"
)

// AlipayOptions 注入支付宝配置和可选宿主依赖；构造不进行 I/O。
type AlipayOptions struct {
	Config   AlipayConfig
	HTTP     *http.Client
	Logger   *slog.Logger
	Observer observability.Observer
	Now      func() time.Time
}

// Alipay 以普通公钥 RSA2 模式验证 App 授权码。
type Alipay struct {
	config   parsedAlipayConfig
	http     *http.Client
	logger   *slog.Logger
	observer *observability.Recorder
	now      func() time.Time
}

// NewAlipay 校验并复制配置；每次 Verify 仅兑换一次授权码，随后尝试一次资料查询。
func NewAlipay(options AlipayOptions) (*Alipay, error) {
	config, err := parseAlipayConfig(options.Config)
	if err != nil {
		return nil, alipayFailure(ErrMisconfigured, "configuration")
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
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Alipay{config: config, http: &isolated, logger: logger, observer: observability.NewRecorder(options.Observer, logger), now: now}, nil
}

// Kind 返回 ALIPAY。
func (*Alipay) Kind() enum.IdentityKind { return enum.IdentityAlipay }

// Verify 严格解码客户端凭证，以授权码兑换并校验支付宝身份；失败返回本包哨兵错误。
func (a *Alipay) Verify(ctx context.Context, credential json.RawMessage) (Identity, error) {
	object, err := alipayObject(credential)
	if err != nil {
		return Identity{}, alipayFailure(ErrMalformedCredential, "credential structure")
	}
	for key := range object {
		if key != "app_id" && key != "code" {
			return Identity{}, alipayFailure(ErrMalformedCredential, "credential field")
		}
	}
	appID, appErr := alipayString(object, "app_id")
	code, codeErr := alipayString(object, "code")
	if appErr != nil || codeErr != nil || appID == "" || strings.TrimSpace(code) == "" {
		return Identity{}, alipayFailure(ErrMalformedCredential, "credential fields")
	}
	if _, ok := a.config.apps[appID]; !ok {
		return Identity{}, alipayFailure(ErrAppNotAllowed, "application not allowed")
	}
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	token, err := a.request(ctx, appID, alipayTokenMethod, url.Values{"code": {code}, "grant_type": {"authorization_code"}}, 10*time.Second)
	if err != nil {
		return Identity{}, err
	}
	union, unionErr := alipayString(token, "union_id")
	open, openErr := alipayString(token, "open_id")
	if unionErr != nil || openErr != nil || union != "" && !validAlipayIdentifier(union) || open != "" && !validAlipayIdentifier(open) {
		return Identity{}, alipayFailure(ErrMisconfigured, "identity fields")
	}
	subject := "unionid:" + union
	if a.config.subjectMode == AlipaySubjectModeOpenID {
		if open == "" {
			return Identity{}, alipayFailure(ErrMisconfigured, "missing openid")
		}
		subject = "openid:" + appID + ":" + open
	} else if union == "" {
		return Identity{}, alipayFailure(ErrMisconfigured, "missing unionid")
	}
	id := Identity{Kind: enum.IdentityAlipay, Subject: subject, UnionID: union}
	if open != "" {
		id.OpenIDs = map[string]string{appID: open}
	}
	// Tokens remain local to this Verify call and never enter Identity or pending
	// proofs. Profile errors cannot invalidate an already verified identity.
	accessToken, tokenErr := alipayString(token, "access_token")
	var profileErr error
	if tokenErr != nil || accessToken == "" || open == "" {
		profileErr = alipayFailure(ErrMisconfigured, "profile prerequisites")
	} else {
		var profile map[string]json.RawMessage
		profile, profileErr = a.request(ctx, appID, alipayProfileMethod, url.Values{"auth_token": {accessToken}}, 3*time.Second)
		if profileErr == nil {
			profileOpen, openErr := alipayString(profile, "open_id")
			name, nameErr := alipayString(profile, "nick_name")
			picture, pictureErr := alipayString(profile, "avatar")
			if openErr != nil || profileOpen != open || nameErr != nil || pictureErr != nil {
				profileErr = alipayFailure(ErrMisconfigured, "profile association or fields")
			} else {
				id.Name, id.Picture = name, picture
			}
		}
	}
	if ctx.Err() != nil {
		return Identity{}, ctx.Err()
	}
	if profileErr != nil {
		a.logger.WarnContext(ctx, "alipay profile unavailable", "stage", "profile")
		a.observer.Observe(ctx, observability.DegradationObservation{Scenario: observability.DegradationAlipayProfileUnavailable, Count: 1})
	}
	return id, nil
}

func validAlipayIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}
