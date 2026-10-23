package idp

import (
	"context"
	"crypto/rsa"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/bbxx111/accountkit/observability"
)

const jwksRefreshThrottle = 60 * time.Second

// jwksCache 是 Apple、Google 与 OIDC 共用的 RSA JWKS 缓存：懒加载、未知 kid 触发
// 至少间隔 60 秒的刷新，刷新失败时继续使用已缓存的密钥并记录固定降级场景。
type jwksCache struct {
	name     string // 仅用于日志
	url      string
	hc       *http.Client
	logger   *slog.Logger
	observer *observability.Recorder
	scenario observability.DegradationScenario
	now      func() time.Time

	mu            sync.Mutex
	keys          map[string]*rsa.PublicKey
	lastRefresh   time.Time
	lastRefreshOK bool
}

func newJWKSCache(name, url string, hc *http.Client, logger *slog.Logger, observer *observability.Recorder, scenario observability.DegradationScenario, now func() time.Time) *jwksCache {
	return &jwksCache{name: name, url: url, hc: hc, logger: logger, observer: observer, scenario: scenario, now: now}
}

// key 返回 kid 对应的公钥。
//
// 节流语义：未知 kid 只在距上次尝试 ≥ 60 秒时才重新拉取（成功或失败都更新时间戳，
// 防止伪造 kid 打爆 JWKS 端点）。kid 最终仍未找到时，只有最近一次刷新成功才是客户端
// 的错（400 unknown kid）；最近一次刷新失败或被节流沿用失败结果时返回
// errJWKSUnavailable（503），否则一次 JWKS 故障会被误判成坏 kid。
func (c *jwksCache) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	cachedFallback := false
	defer func() {
		c.mu.Unlock()
		if cachedFallback && c.observer.Enabled() {
			c.observer.Observe(ctx, observability.DegradationObservation{Scenario: c.scenario, Count: 1})
		}
	}()
	if k, ok := c.keys[kid]; ok {
		cachedFallback = !c.lastRefreshOK
		return k, nil
	}
	now := c.now()
	if c.keys == nil {
		if !c.lastRefresh.IsZero() && now.Sub(c.lastRefresh) < jwksRefreshThrottle {
			return nil, fmt.Errorf("%w: jwks refresh throttled", errJWKSUnavailable)
		}
		if err := c.refreshLocked(ctx); err != nil {
			c.lastRefresh = now
			c.lastRefreshOK = false
			return nil, fmt.Errorf("%w: %v", errJWKSUnavailable, err)
		}
		c.lastRefreshOK = true
	} else if now.Sub(c.lastRefresh) >= jwksRefreshThrottle {
		c.lastRefresh = now
		if err := c.refreshLocked(ctx); err != nil {
			c.lastRefreshOK = false
			c.logger.Warn(c.name+" jwks refresh failed; serving cached keys", "err", err)
		} else {
			c.lastRefreshOK = true
		}
	}
	if k, ok := c.keys[kid]; ok {
		return k, nil
	}
	if !c.lastRefreshOK {
		return nil, fmt.Errorf("%w: unknown kid %q", errJWKSUnavailable, kid)
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

// refreshLocked 拉取并替换 JWKS；调用方持锁；节流时间戳由 key 维护。
//
// 传输错误统一经 redactTransportErr 处理，避免 URL 等细节进入日志或错误消息。
// 拉取 context 与调用方请求脱钩（保留 value，去掉取消/截止），只套固定 10 秒超时：
// 客户端断开不应让这次刷新失败并节流后续 60 秒内的全部登录。
func (c *jwksCache) refreshLocked(ctx context.Context) error {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(fctx, http.MethodGet, c.url, nil)
	if err != nil {
		return redactTransportErr(err)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return redactTransportErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks http %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBody))
	if err != nil {
		return redactTransportErr(err)
	}
	keys, err := parseRSAJWKSet(body)
	if err != nil {
		return err
	}
	c.keys = keys
	return nil
}
