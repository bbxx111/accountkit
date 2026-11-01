// Test-only loopback host. Never deploy this executable.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/bbxx111/accountkit"
	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/sender"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"
)

type options struct{ database, redis, addr, profile, schema, redisPrefix, requiredKinds string }
type capture struct {
	mu    sync.Mutex
	codes map[string]sender.CodeMessage
}

func newCapture() *capture { return &capture{codes: map[string]sender.CodeMessage{}} }
func (s *capture) save(target string, m sender.CodeMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[target+"/"+m.Purpose.String()] = m
	return nil
}
func (s *capture) SendSMS(_ context.Context, target string, m sender.CodeMessage) error {
	return s.save(target, m)
}
func (s *capture) SendEmail(_ context.Context, target string, m sender.CodeMessage) error {
	return s.save(target, m)
}
func (s *capture) get(target, purpose string) (sender.CodeMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.codes[target+"/"+purpose]
	return m, ok
}

type host struct {
	server   *http.Server
	listener net.Listener
	auth     *accountkit.Auth
	pool     *pgxpool.Pool
	redis    *redis.Client
	cancel   context.CancelFunc
	origin   string
}

func (h *host) close() {
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	h.server.Shutdown(ctx)
	h.cancel()
	h.auth.Close()
	h.redis.Close()
	h.pool.Close()
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}
func start(parent context.Context, o options) (h *host, err error) {
	if o.database == "" || o.redis == "" {
		return nil, errors.New("SDK_FIXTURE_DATABASE_URL and SDK_FIXTURE_REDIS_ADDR are required; integration cannot skip")
	}
	settings, err := parseSettings(o)
	if err != nil {
		return nil, err
	}
	if o.addr == "" {
		o.addr = "127.0.0.1:18081"
	}
	addr, _, e := net.SplitHostPort(o.addr)
	if e != nil || addr != "127.0.0.1" {
		return nil, errors.New("fixture must bind 127.0.0.1")
	}
	ln, err := net.Listen("tcp", o.addr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			ln.Close()
		}
	}()
	origin := "http://" + ln.Addr().String()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	providerRouter := chi.NewRouter()
	jwk := map[string]any{"kty": "RSA", "kid": "fixture", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(rsaKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaKey.E)).Bytes())}
	providerRouter.Get("/keys", func(w http.ResponseWriter, r *http.Request) { write(w, map[string]any{"keys": []any{jwk}}) })
	providerRouter.Get("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"issuer": origin, "jwks_uri": origin + "/keys"})
	})
	var live atomic.Pointer[http.Handler]
	initial := http.Handler(providerRouter)
	live.Store(&initial)
	router.Get("/keys", providerRouter.ServeHTTP)
	router.Get("/.well-known/openid-configuration", providerRouter.ServeHTTP)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { (*live.Load()).ServeHTTP(w, r) }), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(ln)
	defer func() {
		if err != nil {
			server.Close()
		}
	}()
	sign := func(claims jwt.MapClaims) (string, error) {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
		token.Header["kid"] = "fixture"
		return token.SignedString(rsaKey)
	}
	router.Get("/__fixture/admin-token", func(w http.ResponseWriter, r *http.Request) {
		role := r.URL.Query().Get("role")
		if role != "operator" && role != "super-admin" {
			http.Error(w, "invalid role", 400)
			return
		}
		roles := []string{role}
		if role == "super-admin" {
			roles = append(roles, "operator")
		}
		token, e := sign(jwt.MapClaims{"iss": origin, "aud": "sdk-admin", "sub": "synthetic-admin", "preferred_username": "fixture", "roles": roles, "iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		if e != nil {
			http.Error(w, "sign failed", 500)
			return
		}
		write(w, map[string]string{"access_token": token})
	})
	capture := newCapture()
	router.Get("/__fixture/code", func(w http.ResponseWriter, r *http.Request) {
		m, ok := capture.get(r.URL.Query().Get("target"), r.URL.Query().Get("purpose"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		write(w, map[string]any{"code": m.Code, "purpose": m.Purpose.String()})
	})
	cfg := accountkit.Config{Schema: settings.schema, KeyPrefix: settings.redisPrefix, RequiredIdentityKinds: settings.required, JWTKeys: map[uint16]*ecdsa.PrivateKey{1: ec}, JWTActiveKey: 1, JWTIssuer: origin, JWTAudience: "sdk-consumer", SubjectCipherKeys: map[uint16][]byte{1: make([]byte, 32)}, SubjectCipherActiveKey: 1, SubjectHMACKeys: map[uint16][]byte{1: make([]byte, 32)}, SubjectHMACActiveKey: 1, CodeCooldown: time.Second, MaintenanceInterval: time.Hour}
	if settings.extended {
		cfg.ProfileAttributes = &user.ProfileAttributesConfig{Schema: []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"theme":{"type":"string"},"nickname":{"type":"string"}},"additionalProperties":false}`)}
	}
	cfg.AppleBundleIDs = []string{"sdk-app"}
	cfg.AppleJWKSURL = origin + "/keys"
	cfg.GoogleClientIDs = []string{"sdk-app"}
	cfg.GoogleJWKSURL = origin + "/keys"
	cfg.FacebookAppID = "sdk-app"
	cfg.FacebookAppSecret = "synthetic-secret"
	cfg.FacebookJWKSURL = origin + "/keys"
	configureUpstreams(router, &cfg, origin, rsaKey)
	cfg.OIDCProviders = []accountkit.OIDCProvider{{Kind: "TEST_OIDC", Issuer: origin, JWKSURL: origin + "/keys", ClientIDs: []string{"sdk-app"}}}
	router.Get("/__fixture/credential", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		kind := q.Get("kind")
		subject := q.Get("subject")
		nonce := q.Get("nonce")
		if subject == "" || nonce == "" {
			http.Error(w, "subject and unique nonce required", 400)
			return
		}
		issuer := origin
		claimNonce := nonce
		switch kind {
		case "WECHAT", "WECHAT_MINI", "QQ", "WEIBO", "ALIPAY", "FACEBOOK_CLASSIC":
			appID := "12345"
			field := "access_token"
			if kind == "WECHAT" {
				appID = "sdk-wechat"
				field = "code"
			}
			if kind == "WECHAT_MINI" {
				appID = "sdk-mini"
				field = "code"
			}
			if kind == "ALIPAY" {
				appID = "2026110100000001"
				field = "code"
			}
			credential := map[string]string{field: "synthetic-" + subject}
			if kind != "FACEBOOK_CLASSIC" {
				credential["app_id"] = appID
			}
			wireKind := kind
			if kind == "WECHAT_MINI" {
				wireKind = "WECHAT"
			}
			if kind == "FACEBOOK_CLASSIC" {
				wireKind = "FACEBOOK"
			}
			write(w, map[string]any{"kind": wireKind, "credential": credential})
			return
		case "APPLE":
			issuer = "https://appleid.apple.com"
			hash := sha256.Sum256([]byte(nonce))
			claimNonce = hex.EncodeToString(hash[:])
		case "GOOGLE":
			issuer = "https://accounts.google.com"
		case "FACEBOOK":
			issuer = "https://www.facebook.com"
		case "TEST_OIDC":
		default:
			http.Error(w, "unsupported controlled kind", 400)
			return
		}
		token, e := sign(jwt.MapClaims{"iss": issuer, "aud": "sdk-app", "sub": subject, "nonce": claimNonce, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix()})
		if e != nil {
			http.Error(w, "sign failed", 500)
			return
		}
		write(w, map[string]any{"kind": kind, "credential": map[string]string{"id_token": token, "nonce": nonce}})
	})
	pc, err := accountkit.PoolConfig(o.database, cfg.Schema)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(parent, pc)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			pool.Close()
		}
	}()
	rdb := redis.NewClient(&redis.Options{Addr: o.redis})
	defer func() {
		if err != nil {
			rdb.Close()
		}
	}()
	var auth *accountkit.Auth
	verifier, err := adminauth.New(parent, adminauth.Config{Issuer: origin, Audience: "sdk-admin", AllowHTTP: true}, adminauth.Options{OnForbidden: func(r *http.Request, p accountkit.AdminPrincipal) {
		if auth != nil {
			auth.RecordAdminForbidden(r, p)
		}
	}})
	if err != nil {
		return nil, err
	}
	auth, err = accountkit.New(cfg, accountkit.Deps{Pool: pool, Redis: rdb, SMSSender: capture, EmailSender: capture, AdminVerifier: verifier, AdminPrincipal: adminauth.PrincipalFrom})
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			auth.Close()
		}
	}()
	if err = auth.Migrate(parent); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	auth.Start(ctx)
	prefix := settings.apiPrefix
	adminPrefix := settings.adminPrefix
	router.Mount(prefix, auth.EndUserHandler())
	router.Group(func(r chi.Router) { r.Use(verifier.Middleware); r.Mount(adminPrefix, auth.AdminHandler()) })
	mode := "embedded-test-host"
	serviceOrigin := origin
	if o.profile == "accountsvc" {
		mode = "accountsvc-development"
		var stop context.CancelFunc
		serviceOrigin, stop, err = runService(parent, ec, o, origin, capture)
		if err != nil {
			cancel()
			return nil, err
		}
		oldCancel := cancel
		cancel = func() { stop(); oldCancel() }
	}
	router.Get("/__fixture/ready", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]string{"origin": serviceOrigin, "control_origin": origin, "api_prefix": prefix, "admin_prefix": adminPrefix, "profile": o.profile, "baseline": "v1.0.0", "mode": mode})
	})
	finalHandler := http.Handler(router)
	live.Store(&finalHandler)
	return &host{server: server, listener: ln, auth: auth, pool: pool, redis: rdb, cancel: cancel, origin: origin}, nil
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	h, err := start(ctx, options{database: os.Getenv("SDK_FIXTURE_DATABASE_URL"), redis: os.Getenv("SDK_FIXTURE_REDIS_ADDR"), addr: os.Getenv("SDK_FIXTURE_ADDR"), profile: os.Getenv("SDK_FIXTURE_PROFILE"), schema: os.Getenv("SDK_FIXTURE_SCHEMA"), redisPrefix: os.Getenv("SDK_FIXTURE_REDIS_PREFIX"), requiredKinds: os.Getenv("SDK_FIXTURE_REQUIRED_KINDS")})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture startup failed:", err)
		os.Exit(1)
	}
	defer h.close()
	fmt.Println("SDK fixture ready:", h.origin)
	<-ctx.Done()
}
