package idp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maxWeiboBody = 64 << 10

func weiboFailure(kind error, stage string) error { return fmt.Errorf("%w: weibo %s", kind, stage) }

// weiboDecimalPattern 只接受规范十进制整数：无前导零、无正号、无 -0，最多 20 位。
var weiboDecimalPattern = regexp.MustCompile(`^(0|-?[1-9][0-9]{0,19})$`)

// weiboDecimal 读取官方文档中类型不固定的字段：JSON 字符串与 JSON 整数等价，
// 数字取原始字面量，不经 float64，避免大 uid 丢失精度。字段缺失返回空串。
func weiboDecimal(object map[string]json.RawMessage, key string) (string, error) {
	raw, exists := object[key]
	if !exists {
		return "", nil
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		if json.Unmarshal(raw, &text) != nil {
			return "", errAlipayJSON
		}
	}
	if !weiboDecimalPattern.MatchString(text) {
		return "", errAlipayJSON
	}
	return text, nil
}

// do 发出一次有界请求并严格解析对象；只返回固定阶段错误：请求可能含 access_token，
// 任何传输错误都不得被包装或记录。4xx 返回状态码与可解析的错误体，由调用方分类。
func (p *Weibo) do(ctx context.Context, method, path string, values url.Values, budget time.Duration) (int, map[string]json.RawMessage, error) {
	child, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	target := p.config.baseURL + path
	var body io.Reader
	if method == http.MethodGet {
		target += "?" + values.Encode()
	} else {
		body = strings.NewReader(values.Encode())
	}
	req, err := http.NewRequestWithContext(child, method, target, body)
	if err != nil {
		return 0, nil, weiboFailure(ErrMisconfigured, "request construction")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := p.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, weiboFailure(ErrUnavailable, "transport")
	}
	defer resp.Body.Close()
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	// 3xx、429 与 5xx 视为依赖不可用，不解析响应体。
	if resp.StatusCode != http.StatusOK && (resp.StatusCode < 400 || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) {
		return 0, nil, weiboFailure(ErrUnavailable, "http status")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxWeiboBody+1))
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	if err != nil || len(raw) > maxWeiboBody {
		return 0, nil, weiboFailure(ErrUnavailable, "response read")
	}
	// alipayObject 是包内通用的严格 JSON 对象解析：拒绝重复键、尾随内容与非对象。
	object, err := alipayObject(raw)
	if err != nil {
		return 0, nil, weiboFailure(ErrUnavailable, "response structure")
	}
	return resp.StatusCode, object, nil
}

// weiboTokenError 仅按已核对的错误码分类；error、request 文本不参与判断也不输出。
func weiboTokenError(code string) error {
	switch code {
	case "21314", "21315", "21316", "21317", "21319", "21327", "21332": // 令牌已使用 / 过期 / 撤销 / 拒绝 / 授权收回 / 过期 / 无效
		return weiboFailure(ErrInvalidCredential, "access token rejected")
	case "10005", "10014", "21321": // 需更高授权 / 应用权限受限 / 未审核应用用户数超限
		return weiboFailure(ErrMisconfigured, "application not permitted")
	}
	return weiboFailure(ErrUnavailable, "upstream error")
}
