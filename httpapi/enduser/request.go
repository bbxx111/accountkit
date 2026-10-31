package enduser

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/apierror"
	"github.com/bbxx111/accountkit/httpapi/internal/jsonbody"
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
var codeIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

const maxDeviceNameRunes = 64

// deviceFrom 读取 X-Device-Id / X-Device-Name。
func deviceFrom(r *http.Request) (user.Device, *apierror.Error) {
	return parseDevice(r, true)
}

// completionDeviceFrom 允许省略首次已经绑定的设备信息；非空字段交给领域层逐项比对。
func completionDeviceFrom(r *http.Request) (user.Device, *apierror.Error) {
	return parseDevice(r, false)
}

func parseDevice(r *http.Request, required bool) (user.Device, *apierror.Error) {
	id := r.Header.Get("X-Device-Id")
	if (required || id != "") && !deviceIDRe.MatchString(id) {
		return user.Device{}, apierror.New(apierror.StatusInvalidArgument, "DEVICE_ID_INVALID", "X-Device-Id header is required and must match ^[A-Za-z0-9._-]{1,64}$")
	}
	name := r.Header.Get("X-Device-Name")
	if required {
		// 初始登录保留既有规范化；补全时原值参与领域层的设备匹配。
		name = strings.TrimSpace(name)
	}
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxDeviceNameRunes || strings.ContainsFunc(name, unicode.IsControl) {
		return user.Device{}, apierror.New(apierror.StatusInvalidArgument, "DEVICE_NAME_INVALID", "X-Device-Name must be <= 64 characters without control characters")
	}
	return user.Device{ID: id, Name: name}, nil
}

// codeCredential 是 PHONE/EMAIL 的 credential 对象。
type codeCredential struct {
	Target string `json:"target"`
	CodeID string `json:"code_id"`
	Code   string `json:"code"`
}

// maxProviderCredentialBytes 是第三方 credential 对象的原始 JSON 上限。
const maxProviderCredentialBytes = 16 << 10

// credential 是统一凭证信封：kind 为类型名，credential 为该类型的凭证对象。
type credential struct {
	Kind       string          `json:"kind"`
	Credential json.RawMessage `json:"credential"`
}

func credentialIncomplete(msg string) *apierror.Error {
	return apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_INCOMPLETE", msg)
}

// parseKind 校验信封完整与 kind 格式，不判断该类型是否已登记。
func (c credential) parseKind() (enum.IdentityKind, *apierror.Error) {
	raw := bytes.TrimSpace(c.Credential)
	if c.Kind == "" || len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", credentialIncomplete("kind and credential are required")
	}
	kind, err := enum.ParseIdentityKind(c.Kind)
	if err != nil {
		return "", apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "kind must match ^[A-Z][A-Z0-9_]{1,31}$")
	}
	return kind, nil
}

// isAnchor 报告信封是否声明 PHONE/EMAIL；用于同时接受两类凭证的端点分流。
func (c credential) isAnchor() bool { return enum.IdentityKind(c.Kind).IsAnchor() }

// anchor 只接受 PHONE/EMAIL，并严格解码 {target, code_id, code}。
func (c credential) anchor() (enum.IdentityKind, codeCredential, *apierror.Error) {
	kind, e := c.parseKind()
	if e != nil {
		return "", codeCredential{}, e
	}
	if !kind.IsAnchor() {
		return "", codeCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_KIND_NOT_ALLOWED", "only PHONE or EMAIL credentials are accepted here")
	}
	var cc codeCredential
	if err := decodeObject(c.Credential, &cc); err != nil {
		return "", codeCredential{}, credentialIncomplete("credential must be an object with target, code_id and code")
	}
	if cc.Target == "" || cc.Code == "" {
		return "", codeCredential{}, credentialIncomplete("target and code are required")
	}
	if !codeIDRe.MatchString(cc.CodeID) {
		return "", codeCredential{}, apierror.New(apierror.StatusInvalidArgument, "INVALID_ARGUMENT", "code_id must be 32 lowercase hexadecimal characters")
	}
	return kind, cc, nil
}

// provider 只接受第三方类型；credential 原样交给该类型的提供方解析。
func (c credential) provider() (user.IdpCredential, *apierror.Error) {
	kind, e := c.parseKind()
	if e != nil {
		return user.IdpCredential{}, e
	}
	if kind.IsAnchor() {
		return user.IdpCredential{}, apierror.New(apierror.StatusInvalidArgument, "CREDENTIAL_KIND_NOT_ALLOWED", "only third-party credentials are accepted here")
	}
	raw := bytes.TrimSpace(c.Credential)
	if raw[0] != '{' || len(raw) > maxProviderCredentialBytes {
		return user.IdpCredential{}, credentialIncomplete("credential must be a JSON object of at most 16 KiB")
	}
	return user.IdpCredential{Kind: kind, Credential: json.RawMessage(bytes.Clone(raw))}, nil
}

// decodeObject 严格解码单个 JSON 对象：拒绝非对象、未知字段与尾随内容。
func decodeObject(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errMalformedBody
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errMalformedBody
	}
	return nil
}

// parseChannel 解析 "PHONE" / "EMAIL"。
func parseChannel(s string) (enum.IdentityKind, *apierror.Error) {
	k, err := enum.ParseIdentityKind(s)
	if err != nil || !k.IsChannel() {
		return "", apierror.New(apierror.StatusInvalidArgument, "CHANNEL_INVALID", "channel must be PHONE or EMAIL")
	}
	return k, nil
}
