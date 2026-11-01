package main

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"github.com/bbxx111/accountkit"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
	"time"
)

func configureUpstreams(router chi.Router, cfg *accountkit.Config, origin string, key *rsa.PrivateKey) {
	cfg.WeChatApps = []accountkit.WeChatApp{{AppID: "sdk-wechat", Secret: "synthetic-secret"}}
	cfg.WeChatMiniProgramApps = []accountkit.WeChatApp{{AppID: "sdk-mini", Secret: "synthetic-secret"}}
	cfg.WeChatAPIBaseURL = origin
	cfg.QQAppIDs = []string{"12345"}
	cfg.QQAPIBaseURL = origin
	cfg.WeiboAppIDs = []string{"12345"}
	cfg.WeiboAPIBaseURL = origin
	cfg.FacebookGraphAPIBaseURL = origin
	pub, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	cfg.AlipayApps = []accountkit.AlipayApp{{AppID: "2026110100000001", PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), AlipayPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pub}))}}
	cfg.AlipayGatewayURL = origin + "/gateway.do"
	wx := func(w http.ResponseWriter, r *http.Request) {
		subject := r.URL.Query().Get("code")
		if subject == "" {
			subject = r.URL.Query().Get("js_code")
		}
		if !strings.HasPrefix(subject, "synthetic-") {
			write(w, map[string]int{"errcode": 40029})
			return
		}
		write(w, map[string]any{"openid": subject, "unionid": subject})
	}
	router.Get("/sns/oauth2/access_token", wx)
	router.Get("/sns/jscode2session", wx)
	router.Get("/oauth2.0/me", func(w http.ResponseWriter, r *http.Request) {
		s := r.URL.Query().Get("access_token")
		if !strings.HasPrefix(s, "synthetic-") {
			write(w, map[string]int{"error": 100014})
			return
		}
		write(w, map[string]string{"client_id": "12345", "openid": s, "unionid": s})
	})
	router.Get("/user/get_user_info", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"ret": 0, "nickname": "Synthetic"})
	})
	router.Post("/oauth2/get_token_info", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if !strings.HasPrefix(r.Form.Get("access_token"), "synthetic-") {
			write(w, map[string]int{"error_code": 21327})
			return
		}
		write(w, map[string]any{"appkey": "12345", "uid": "123456789", "expire_in": 300})
	})
	router.Get("/2/users/show.json", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]string{"screen_name": "Synthetic"}) })
	router.Get("/v26.0/debug_token", func(w http.ResponseWriter, r *http.Request) {
		s := r.URL.Query().Get("input_token")
		write(w, map[string]any{"data": map[string]any{"app_id": "sdk-app", "type": "USER", "is_valid": strings.HasPrefix(s, "synthetic-"), "user_id": s, "expires_at": time.Now().Add(time.Hour).Unix()}})
	})
	router.Get("/v26.0/me", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]string{"id": r.URL.Query().Get("access_token"), "name": "Synthetic"})
	})
	router.Post("/gateway.do", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		node := "alipay_system_oauth_token_response"
		payload := map[string]any{"access_token": "synthetic-alipay", "expires_in": 300, "union_id": r.Form.Get("code"), "open_id": r.Form.Get("code")}
		if r.Form.Get("method") == "alipay.user.info.share" {
			node = "alipay_user_info_share_response"
			payload = map[string]any{"code": "10000", "nick_name": "Synthetic"}
		}
		raw, _ := json.Marshal(payload)
		digest := sha256.Sum256(raw)
		sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if e != nil {
			http.Error(w, "sign failure", 500)
			return
		}
		write(w, map[string]any{node: json.RawMessage(raw), "sign": base64.StdEncoding.EncodeToString(sig), "sign_type": "RSA2"})
	})
}
