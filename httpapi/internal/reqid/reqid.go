// Package reqid 是 C 端与管理面共用的请求 id 中间件：解析/生成 id，写回 X-Request-Id 响应头并放入 ctx。
package reqid

import (
	"context"
	"net/http"
)

type ctxKey struct{}

// Middleware 用 resolve 取请求 id（宿主注入的 Deps.RequestID：读 X-Request-Id 或生成），写响应头并放入 ctx。
func Middleware(resolve func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := resolve(r)
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
		})
	}
}

// From 取出 Middleware 放入的请求 id；没有则为空串。
func From(ctx context.Context) string {
	s, _ := ctx.Value(ctxKey{}).(string)
	return s
}
