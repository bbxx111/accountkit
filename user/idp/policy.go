package idp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func configurationFingerprint(kind, endpoint string, applications []string) string {
	sort.Strings(applications)
	b, _ := json.Marshal(struct {
		Kind, Endpoint string
		Applications   []string
	}{kind, endpoint, applications})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// PolicyFingerprint 标识允许的应用集合，不包含 AppSecret。
func (w *WeChat) PolicyFingerprint() string {
	apps := make([]string, 0, len(w.secrets))
	for app := range w.secrets {
		apps = append(apps, app)
	}
	return configurationFingerprint("wechat", w.base, apps)
}

// PolicyFingerprint 标识 Apple 应用允许列表和验证配置。
func (a *Apple) PolicyFingerprint() string {
	apps := make([]string, 0, len(a.bundleIDs))
	for app := range a.bundleIDs {
		apps = append(apps, app)
	}
	return configurationFingerprint("apple", a.jwksURL, apps)
}

// PolicyFingerprint 标识 Google client ID 允许列表和 JWKS 地址。
func (g *Google) PolicyFingerprint() string {
	apps := make([]string, 0, len(g.clientIDs))
	for app := range g.clientIDs {
		apps = append(apps, app)
	}
	return configurationFingerprint("google", g.jwksURL, apps)
}
