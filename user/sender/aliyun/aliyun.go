// Package aliyun 提供可选的阿里云短信验证码发送器（SendSms，RPC 风格 HMAC-SHA1 签名，
// 不依赖阿里云 SDK）。首版只发送中国大陆号码；发送器不自动重试，也不输出手机号、
// 验证码、AccessKey Secret 或服务商返回的消息原文。
package aliyun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user/sender"
)

// DefaultEndpoint 是短信服务的公网 endpoint。
const DefaultEndpoint = "https://dysmsapi.aliyuncs.com/"

const (
	defaultTimeout = 10 * time.Second
	maxResponse    = 64 * 1024
	mainlandPrefix = "+86"
)

// Config 指定阿里云凭据、签名与四个用途的模板 code（模板变量固定为 ${code}）。
// 多个用途可以使用同一模板。Endpoint 为空时使用 DefaultEndpoint；仅回环地址允许 HTTP（测试用）。
type Config struct {
	AccessKeyID     string
	AccessKeySecret string
	SignName        string
	Templates       map[enum.CodePurpose]string
	Endpoint        string
	HTTPClient      *http.Client
	Timeout         time.Duration // 单次请求，默认 10s
	// Logger 可选；发送失败时以 Warn 记录阶段、HTTP 状态、服务商错误码与 RequestId，
	// 不记录手机号、验证码、Secret 或服务商消息原文。nil 时不记录。
	Logger *slog.Logger
}

// Sender 复用不可变配置，并发安全。
type Sender struct {
	cfg       Config
	endpoint  string
	templates map[enum.CodePurpose]string
	client    http.Client
	now       func() time.Time
}

var purposes = []enum.CodePurpose{enum.PurposeSignIn, enum.PurposeBind, enum.PurposeReauth, enum.PurposeSignInComplete}

// New 只校验和复制配置，不进行网络 I/O。错误只包含配置项名。
func New(cfg Config) (*Sender, error) {
	for name, v := range map[string]string{"AccessKeyID": cfg.AccessKeyID, "AccessKeySecret": cfg.AccessKeySecret, "SignName": cfg.SignName} {
		if strings.TrimSpace(v) == "" {
			return nil, fmt.Errorf("aliyun: %s is required", name)
		}
	}
	templates := make(map[enum.CodePurpose]string, len(purposes))
	for _, p := range purposes {
		code := strings.TrimSpace(cfg.Templates[p])
		if code == "" {
			return nil, fmt.Errorf("aliyun: template for %s is required", p)
		}
		templates[p] = code
	}
	if cfg.Timeout < 0 {
		return nil, errors.New("aliyun: Timeout must not be negative")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if err := ValidateEndpoint(endpoint, true); err != nil {
		return nil, err
	}
	s := &Sender{cfg: cfg, endpoint: endpoint, templates: templates, now: time.Now}
	if cfg.HTTPClient != nil {
		s.client = *cfg.HTTPClient
	}
	s.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return s, nil
}

// ValidateEndpoint 要求绝对 HTTPS URL，且不带 userinfo、查询串或 fragment；allowLoopbackHTTP 时回环地址可用 HTTP。
// 错误不包含 endpoint 内容。
func ValidateEndpoint(endpoint string, allowLoopbackHTTP bool) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("aliyun: invalid Endpoint")
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && allowLoopbackHTTP && isLoopback(u.Hostname()):
	default:
		return errors.New("aliyun: Endpoint must use HTTPS")
	}
	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// mainland 返回 +86 后的 11 位手机号；非中国大陆手机号返回空串。
func mainland(e164 string) string {
	rest, ok := strings.CutPrefix(e164, mainlandPrefix)
	// 中国大陆手机号为 1 开头的 11 位；固定电话等同长号码不能接收短信。
	if !ok || len(rest) != 11 || rest[0] != '1' {
		return ""
	}
	for _, c := range rest {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return rest
}

// SupportsTarget 声明首版只支持中国大陆号码；发码流程据此在占用额度之前拒绝其他号码。
func (s *Sender) SupportsTarget(e164 string) bool { return mainland(e164) != "" }

func validCode(code string) bool {
	if code == "" {
		return false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

var safeToken = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// token 只保留服务商错误码与 RequestId 中的安全字符，供运维定位。
func token(v string) string {
	if safeToken.MatchString(v) {
		return v
	}
	return "invalid"
}

// SendSMS 发送一条验证码短信。只有 HTTP 200 且 Code 为 OK 时成功；其余结果返回 sender.ErrUnavailable，
// 不重试、不跟随重定向。调用方取消保留 context 错误。
func (s *Sender) SendSMS(ctx context.Context, e164 string, m sender.CodeMessage) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", sender.ErrUnavailable, err)
	}
	number := mainland(e164)
	if number == "" {
		return sender.ErrTargetNotSupported
	}
	template, ok := s.templates[m.Purpose]
	if !ok || !validCode(m.Code) {
		return fmt.Errorf("%w: invalid code message", sender.ErrUnavailable)
	}
	param, _ := json.Marshal(map[string]string{"code": m.Code})
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("%w: nonce", sender.ErrUnavailable)
	}
	form := url.Values{
		"AccessKeyId":      {s.cfg.AccessKeyID},
		"Action":           {"SendSms"},
		"Format":           {"JSON"},
		"PhoneNumbers":     {number},
		"RegionId":         {"cn-hangzhou"},
		"SignName":         {s.cfg.SignName},
		"SignatureMethod":  {"HMAC-SHA1"},
		"SignatureNonce":   {hex.EncodeToString(nonce)},
		"SignatureVersion": {"1.0"},
		"TemplateCode":     {template},
		"TemplateParam":    {string(param)},
		"Timestamp":        {s.now().UTC().Format("2006-01-02T15:04:05Z")},
		"Version":          {"2017-05-25"},
	}
	form.Set("Signature", sign(http.MethodPost, s.cfg.AccessKeySecret, form))

	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, s.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return s.fail(ctx, failure{stage: "build request"})
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", sender.ErrUnavailable, ctxErr)
		}
		// 传输错误文本可能包含 endpoint 等细节，只返回阶段。
		return s.fail(ctx, failure{stage: "transport"})
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(body) > maxResponse {
		return s.fail(ctx, failure{stage: "read response", status: resp.StatusCode})
	}
	// 网关的鉴权与签名错误（如 SignatureDoesNotMatch）以非 200 状态返回同样的 JSON 结构，一并解析以便定位。
	var result struct {
		Code      string `json:"Code"`
		RequestID string `json:"RequestId"`
	}
	parsed := json.Unmarshal(body, &result) == nil && result.Code != ""
	switch {
	case resp.StatusCode == http.StatusOK && parsed && result.Code == "OK":
		return nil
	case !parsed:
		return s.fail(ctx, failure{stage: "malformed response", status: resp.StatusCode})
	default:
		// 只保留错误码与 RequestId；Message 可能包含号码等信息，不读取。
		return s.fail(ctx, failure{stage: "provider", status: resp.StatusCode, code: token(result.Code), requestID: token(result.RequestID)})
	}
}

type failure struct {
	stage           string
	status          int
	code, requestID string
}

// fail 记录脱敏的失败信息并返回投递不可用错误；错误与日志都不含号码、验证码或 Secret。
func (s *Sender) fail(ctx context.Context, f failure) error {
	if s.cfg.Logger != nil {
		attrs := []any{"stage", f.stage}
		if f.status != 0 {
			attrs = append(attrs, "http_status", f.status)
		}
		if f.code != "" {
			attrs = append(attrs, "provider_code", f.code, "request_id", f.requestID)
		}
		s.cfg.Logger.WarnContext(ctx, "aliyun: SMS delivery failed", attrs...)
	}
	switch {
	case f.code != "":
		return fmt.Errorf("%w: provider code %s request %s (http status %d)", sender.ErrUnavailable, f.code, f.requestID, f.status)
	case f.status != 0 && f.status != http.StatusOK:
		return fmt.Errorf("%w: %s (http status %d)", sender.ErrUnavailable, f.stage, f.status)
	default:
		return fmt.Errorf("%w: %s", sender.ErrUnavailable, f.stage)
	}
}
