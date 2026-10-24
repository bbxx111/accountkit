package idp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const maxQQBody = 64 << 10

func qqFailure(kind error, stage string) error { return fmt.Errorf("%w: qq %s", kind, stage) }

// get 只返回固定阶段错误：请求 URL 含 access_token，任何传输错误都不得被包装或记录。
func (q *QQ) get(ctx context.Context, path string, query url.Values, budget time.Duration) (map[string]json.RawMessage, error) {
	child, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	req, err := http.NewRequestWithContext(child, http.MethodGet, q.config.baseURL+path+"?"+query.Encode(), nil)
	if err != nil {
		return nil, qqFailure(ErrMisconfigured, "request construction")
	}
	resp, err := q.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, qqFailure(ErrUnavailable, "transport")
	}
	defer resp.Body.Close()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if resp.StatusCode != http.StatusOK {
		return nil, qqFailure(ErrUnavailable, "http status")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxQQBody+1))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || len(body) > maxQQBody {
		return nil, qqFailure(ErrUnavailable, "response read")
	}
	// alipayObject 是包内通用的严格 JSON 对象解析：拒绝重复键、尾随内容与非对象。
	object, err := alipayObject(body)
	if err != nil {
		return nil, qqFailure(ErrUnavailable, "response structure")
	}
	return object, nil
}

// qqMeError 仅按已核对的公共返回码分类；error_description 不参与判断也不输出。
func qqMeError(code int64) error {
	switch code {
	case 100013, 100014, 100015, 100016: // access token 非法 / 过期 / 已废除 / 校验失败
		return qqFailure(ErrInvalidCredential, "access token rejected")
	case 100048: // 未开通 UnionID 打通权限
		return qqFailure(ErrMisconfigured, "unionid not enabled")
	}
	return qqFailure(ErrUnavailable, "upstream error")
}
