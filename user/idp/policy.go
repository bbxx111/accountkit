package idp

import (
	"crypto/sha256"
	"crypto/x509"
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

// PolicyFingerprint 标识两组应用归属、主标识模式和 API 地址，不包含 AppSecret。
func (w *WeChat) PolicyFingerprint() string {
	apps := make([]string, 0, len(w.secrets))
	for app := range w.secrets {
		group := "oauth:"
		if w.miniPrograms[app] {
			group = "mini-program:"
		}
		apps = append(apps, group+app)
	}
	return configurationFingerprint("wechat:"+string(w.mode), w.base, apps)
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
func (g *Google) PolicyFingerprint() string { return g.v.fingerprint(g.jwksURL) }

// PolicyFingerprint 标识 App ID、Graph 版本与地址、JWKS 地址，不含 App Secret。
func (f *Facebook) PolicyFingerprint() string {
	return f.v.fingerprint(f.version + "\n" + f.graph + "\n" + f.jwksURL)
}

// PolicyFingerprint 包含应用集合、模式、网关和规范公钥摘要，不含应用私钥。
func (a *Alipay) PolicyFingerprint() string {
	apps := make([]string, 0, len(a.config.apps))
	for appID, app := range a.config.apps {
		der, _ := x509.MarshalPKIXPublicKey(app.publicKey)
		digest := sha256.Sum256(der)
		apps = append(apps, appID+":"+hex.EncodeToString(digest[:]))
	}
	return configurationFingerprint("alipay:"+string(a.config.subjectMode), a.config.gatewayURL, apps)
}
