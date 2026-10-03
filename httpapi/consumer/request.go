package consumer

import (
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/jsonbody"
	"github.com/bbxx111/accountkit/user"
)

// errMalformedBody：body 缺失、非法 JSON、未知字段、超长或有多余内容。
var errMalformedBody = jsonbody.ErrMalformed

// decodeJSON 以 64 KiB 上限、严格模式解码 body；任何问题都返回 errMalformedBody（细节记 Debug 日志）。
func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if err := jsonbody.Decode(w, r, dst, h.d.MaxBodyBytes); err != nil {
		h.d.Logger.Debug("malformed request body", "request_id", requestIDFrom(r.Context()), "err", err)
		return errMalformedBody
	}
	return nil
}

func malformed() *apierror.Error { return jsonbody.Malformed() }

var deviceIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

const maxDeviceNameRunes = 64

// deviceFrom 读取 X-Device-Id / X-Device-Name。
func deviceFrom(r *http.Request) (user.Device, *apierror.Error) {
	id := r.Header.Get("X-Device-Id")
	if !deviceIDRe.MatchString(id) {
		return user.Device{}, apierror.New(apierror.StatusInvalidArgument, "DEVICE_ID_INVALID", "X-Device-Id header is required and must match ^[A-Za-z0-9._-]{1,64}$")
	}
	name := strings.TrimSpace(r.Header.Get("X-Device-Name"))
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxDeviceNameRunes || strings.ContainsFunc(name, unicode.IsControl) {
		return user.Device{}, apierror.New(apierror.StatusInvalidArgument, "DEVICE_NAME_INVALID", "X-Device-Name must be <= 64 characters without control characters")
	}
	return user.Device{ID: id, Name: name}, nil
}

// codeCredential 是 phone/email 凭证成员。
type codeCredential struct {
	Target string `json:"target"`
	Code   string `json:"code"`
}

// wechatCredential / appleCredential 是 §2.1 的第三方成员；解析留给 idp 包。
type wechatCredential struct {
	AppID string `json:"app_id"`
	Code  string `json:"code"`
}

type appleCredential struct {
	IDToken string `json:"id_token"`
	Nonce   string `json:"nonce"`
}

// credential 是 §2.1 的凭证 oneof；成员为指针，显式 null 等同缺失。
type credential struct {
	Phone  *codeCredential   `json:"phone"`
	Email  *codeCredential   `json:"email"`
	Wechat *wechatCredential `json:"wechat"`
	Apple  *appleCredential  `json:"apple"`
}

func (c credential) present() int {
	n := 0
	for _, p := range []bool{c.Phone != nil, c.Email != nil, c.Wechat != nil, c.Apple != nil} {
		if p {
			n++
		}
	}
	return n
}

func oneofError() *apierror.Error {
	return apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_ONEOF", "exactly one of phone, email, wechat, apple must be present")
}

// anchor 校验恰好一个成员，且只接受 phone/email。
func (c credential) anchor() (enum.IdentityKind, codeCredential, *apierror.Error) {
	if c.present() != 1 {
		return 0, codeCredential{}, oneofError()
	}
	if c.Wechat != nil || c.Apple != nil {
		return 0, codeCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_KIND_NOT_ALLOWED", "only phone or email credentials are accepted here")
	}
	kind, cc := enum.IdentityPhone, c.Phone
	if c.Email != nil {
		kind, cc = enum.IdentityEmail, c.Email
	}
	if cc.Target == "" || cc.Code == "" {
		return 0, codeCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_INCOMPLETE", "target and code are required")
	}
	return kind, *cc, nil
}

// idp 校验恰好一个成员，且只接受 wechat/apple。
func (c credential) idp() (user.IdpCredential, *apierror.Error) {
	if c.present() != 1 {
		return user.IdpCredential{}, oneofError()
	}
	switch {
	case c.Wechat != nil:
		if c.Wechat.AppID == "" || c.Wechat.Code == "" {
			return user.IdpCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_INCOMPLETE", "app_id and code are required")
		}
		return user.IdpCredential{Kind: enum.IdentityWeChat, AppID: c.Wechat.AppID, Code: c.Wechat.Code}, nil
	case c.Apple != nil:
		if c.Apple.IDToken == "" || c.Apple.Nonce == "" {
			return user.IdpCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_INCOMPLETE", "id_token and nonce are required")
		}
		return user.IdpCredential{Kind: enum.IdentityApple, IDToken: c.Apple.IDToken, Nonce: c.Apple.Nonce}, nil
	}
	return user.IdpCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_KIND_NOT_ALLOWED", "only wechat or apple credentials are accepted here")
}

// parseChannel 解析 "PHONE" / "EMAIL"。
func parseChannel(s string) (enum.IdentityKind, *apierror.Error) {
	k, err := enum.ParseIdentityKind(s)
	if err != nil || !k.IsChannel() {
		return 0, apierror.New(apierror.StatusInvalidArgument, "CHANNEL_INVALID", "channel must be PHONE or EMAIL")
	}
	return k, nil
}
