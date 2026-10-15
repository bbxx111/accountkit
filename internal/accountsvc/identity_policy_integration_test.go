package accountsvc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/jackc/pgx/v5"
)

// This local protocol server exercises accountsvc's actual provider assembly.
// It does not assert compatibility with a live external WeChat deployment.
func (f *integrationFixture) localWeChat() {
	f.t.Helper()
	const app, secret, subject = "service-test-app", "synthetic-wechat-secret", "synthetic-wechat-subject"
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/sns/oauth2/access_token" || q.Get("appid") != app || q.Get("secret") != secret || q.Get("code") == "" || q.Get("grant_type") != "authorization_code" {
			http.Error(w, "invalid fixture exchange", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"unionid": subject, "openid": "synthetic-wechat-openid"})
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()
	f.t.Cleanup(srv.Close)
	f.env["ACCOUNTKIT_WECHAT_APPS"] = app + ":" + secret
	f.env["ACCOUNTKIT_WECHAT_API_BASE_URL"] = srv.URL
	f.secrets = append(f.secrets, secret, subject, "synthetic-wechat-openid")
}

func providerSignInBody(t *testing.T, code string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"wechat": map[string]string{"app_id": "service-test-app", "code": code}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestServiceIntegrationIdentityPolicy(t *testing.T) {
	t.Run("pure_wechat_without_smtp", servicePureWeChat)
	t.Run("gateway_completion", serviceGatewayCompletion)
	t.Run("active_flow_key_versions", serviceMigrationFlowKeys)
	t.Run("migration_dependency_failure", serviceMigrationDependencies)
	for _, kind := range []string{"initialization", "legacy_engine"} {
		t.Run("migration_lock_"+kind, func(t *testing.T) { serviceMigrationLock(t, kind) })
	}
}

func serviceMigrationDependencies(t *testing.T) {
	f := newIntegration(t)
	proxy := newRedisProxy(t, f.redisURL)
	redisURL, err := url.Parse(f.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	redisURL.Host = proxy.ln.Addr().String()
	minimal := map[string]string{"ACCOUNTSVC_STARTUP_TIMEOUT": "400ms", "ACCOUNTSVC_SMTP_PORT": "invalid-unused", "ACCOUNTKIT_ENABLED_IDENTITY_KINDS": "PHONE,WECHAT", "ACCOUNTKIT_REQUIRED_IDENTITY_KINDS": "PHONE", "ACCOUNTKIT_WECHAT_APPS": "", "ACCOUNTSVC_REDIS_URL": redisURL.String()}
	proxy.fail(true)
	if _, err := f.command(minimal, "migrate"); err == nil {
		t.Fatal("migration ignored unavailable Redis")
	}
	proxy.fail(false)
	databaseURL, err := url.Parse(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	databaseURL.Host = freeAddress(t)
	minimal["ACCOUNTSVC_DATABASE_URL"] = databaseURL.String()
	if _, err := f.command(minimal, "migrate"); err == nil {
		t.Fatal("migration ignored unavailable PostgreSQL")
	}
	var namespaces int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_namespace WHERE nspname=$1", f.schema).Scan(&namespaces); err != nil || namespaces != 0 {
		t.Fatal("dependency failure performed migration DDL")
	}
	delete(minimal, "ACCOUNTSVC_DATABASE_URL")
	minimal["ACCOUNTSVC_STARTUP_TIMEOUT"] = "5s"
	if _, err := f.command(minimal, "migrate"); err != nil {
		t.Fatal("migration did not recover after its dependencies became available")
	}
}

func servicePureWeChat(t *testing.T) {
	f := newIntegration(t)
	f.localWeChat()
	f.env["ACCOUNTKIT_ENABLED_IDENTITY_KINDS"] = "WECHAT"
	f.env["ACCOUNTKIT_REQUIRED_IDENTITY_KINDS"] = "[]"
	for _, key := range []string{"HOST", "PORT", "FROM", "USERNAME", "PASSWORD", "TLS_MODE", "TIMEOUT"} {
		f.env["ACCOUNTSVC_SMTP_"+key] = "malformed-disabled-smtp"
	}
	p := f.start(nil)
	headers := map[string]string{"X-Device-Id": "provider-device", "X-Device-Name": "Provider phone"}
	f.secrets = append(f.secrets, "private-initial-code", "private-relogin-code")
	h, pair := transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:signInWithIdp", "application/json", providerSignInBody(t, "private-initial-code"), headers, 200)
	if pair["scope"] != "user" || pair["is_new_user"] != true || pair["next_action"] != nil || h.Get("Cache-Control") != "no-store" {
		t.Fatal("pure provider sign-in did not register with a normal private token response")
	}
	access, refresh := pair["access_token"].(string), pair["refresh_token"].(string)
	f.secrets = append(f.secrets, access, refresh)
	_, relogin := transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:signInWithIdp", "application/json", providerSignInBody(t, "private-relogin-code"), headers, 200)
	if relogin["user_id"] != pair["user_id"] || relogin["is_new_user"] != false || relogin["access_token"] == access {
		t.Fatal("pure provider re-login did not retain the account and replace the session")
	}
	f.secrets = append(f.secrets, relogin["access_token"].(string), relogin["refresh_token"].(string))
	f.introspect(p, access, false)
	f.introspect(p, relogin["access_token"].(string), true)
	f.call("POST", p.baseURL+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": "disabled@example.test"}, 400)
	select {
	case <-f.smtp.messages:
		t.Fatal("disabled EMAIL reached SMTP")
	default:
	}
	p.stop()
	f.assertAuditPrivate()
}

func serviceGatewayCompletion(t *testing.T) {
	f := newIntegration(t)
	f.localWeChat()
	f.env["ACCOUNTKIT_ENABLED_IDENTITY_KINDS"] = "EMAIL,WECHAT"
	f.env["ACCOUNTKIT_REQUIRED_IDENTITY_KINDS"] = "EMAIL"
	f.env["ACCOUNTKIT_CODE_COOLDOWN"] = "1m"
	p := f.start(map[string]string{"ACCOUNTSVC_TLS_ENABLED": "false", "ACCOUNTSVC_TLS_CERT_FILE": "", "ACCOUNTSVC_TLS_KEY_FILE": "", "ACCOUNTSVC_TRUSTED_PROXY_CIDRS": "127.0.0.1/32"})
	public := newTestGateway(t, p.baseURL, f.cert, false)
	internal := newTestGateway(t, p.baseURL, f.cert, true)
	headers := map[string]string{"X-Device-Id": "completion-gateway-device", "X-Device-Name": "Completion phone", "X-Request-Id": "completion-initial", "X-Forwarded-For": "198.51.100.27", "Forwarded": "for=198.51.100.27"}
	f.secrets = append(f.secrets, "private-pending-code")
	h, pending := transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:signInWithIdp", "application/json", providerSignInBody(t, "private-pending-code"), headers, 200)
	token, ok := pending["sign_in_completion_token"].(string)
	if !ok || token == "" || pending["next_action"] != "COMPLETE_IDENTITY" || pending["user_id"] != nil || pending["access_token"] != nil || h.Get("Cache-Control") != "no-store" {
		t.Fatal("provider sign-in did not return a private pending-only result")
	}
	f.secrets = append(f.secrets, token)
	for _, table := range []string{"user_account", "identity", "session"} {
		var n int
		if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatal("pending sign-in created account resources")
		}
	}
	for _, path := range []string{"/v1/accounts:sendSignInCompletionCode", "/v1/accounts:completeSignIn", "/v1/introspect", "/healthz", "/readyz", "/admin/v1/users", "/admin/v1/users:completeSignIn"} {
		transportRequest(t, f.client, "POST", public.server.URL+path, "application/json", `{}`, headers, 404)
	}
	if public.transport.calls.Load() != 0 {
		t.Fatal("public denied route reached the service")
	}
	const target = "completion-gateway@example.test"
	f.secrets = append(f.secrets, target)
	send := func(ticket, channel string, want int) (http.Header, map[string]any) {
		raw, _ := json.Marshal(map[string]string{"sign_in_completion_token": ticket, "channel": channel, "target": target})
		return transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:sendSignInCompletionCode", "application/json", string(raw), headers, want)
	}
	_, invalid := send("private-invalid-completion-token", "EMAIL", 400)
	if detail, _ := invalid["error"].(map[string]any); detail["reason"] != "SIGN_IN_COMPLETION_INVALID" {
		t.Fatal("gateway did not preserve invalid ticket business error")
	}
	_, disabled := send(token, "PHONE", 400)
	if detail, _ := disabled["error"].(map[string]any); detail["reason"] != "IDENTITY_KIND_NOT_ENABLED" {
		t.Fatal("gateway did not preserve disabled identity error")
	}
	headers["X-Request-Id"] = "completion-send"
	h, challenge := send(token, "EMAIL", 200)
	if h.Get("Cache-Control") != "no-store" {
		t.Fatal("completion code challenge is cacheable")
	}
	f.rememberChallenge(map[string]string{"target": target}, challenge)
	m := f.smtp.mail(t)
	if m.target != target || !strings.Contains(m.body, "完成登录并关联身份") {
		t.Fatal("completion SMTP recipient or purpose mismatch")
	}
	f.secrets = append(f.secrets, m.code, "private-invalid-completion-token")
	h, limited := send(token, "EMAIL", 429)
	if detail, _ := limited["error"].(map[string]any); detail["reason"] != "COOLDOWN" || h.Get("Retry-After") == "" {
		t.Fatal("gateway lost completion cooldown Retry-After")
	}
	raw, _ := json.Marshal(map[string]any{"sign_in_completion_token": token, "email": map[string]string{"code_id": f.codeID(target), "target": target, "code": m.code}})
	headers["X-Request-Id"] = "completion-final"
	h, pair := transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:completeSignIn", "application/json", string(raw), headers, 200)
	if pair["scope"] != "user" || pair["is_new_user"] != true || pair["next_action"] != nil || h.Get("Cache-Control") != "no-store" {
		t.Fatal("gateway completion did not return normal private tokens")
	}
	f.secrets = append(f.secrets, pair["access_token"].(string), pair["refresh_token"].(string))
	_, replay := transportRequest(t, f.client, "POST", public.server.URL+"/v1/users:completeSignIn", "application/json", string(raw), headers, 200)
	if replay["access_token"] != pair["access_token"] || replay["refresh_token"] != pair["refresh_token"] || replay["user_id"] != pair["user_id"] {
		t.Fatal("gateway retried completion did not return the same result")
	}
	headers["Authorization"] = "Bearer " + pair["access_token"].(string)
	_, sessions := transportRequest(t, f.client, "GET", public.server.URL+"/v1/users/me/sessions", "", "", headers, 200)
	items, _ := sessions["sessions"].([]any)
	if len(items) != 1 {
		t.Fatal("completion replay created another session")
	}
	session, _ := items[0].(map[string]any)
	if session["device_id"] != "completion-gateway-device" || session["device_name"] != "Completion phone" {
		t.Fatal("completion changed the initial device metadata")
	}
	form := "token=" + pair["access_token"].(string)
	h, denied := transportRequest(t, f.client, "POST", internal.server.URL+"/v1/introspect", "application/x-www-form-urlencoded", form, headers, 401)
	if denied["error"] != "invalid_client" || !strings.HasPrefix(h.Get("WWW-Authenticate"), "Basic ") {
		t.Fatal("consumer token bypassed introspection client authentication")
	}
	headers["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte("business:"+f.secrets[1]))
	_, active := transportRequest(t, f.client, "POST", internal.server.URL+"/v1/introspect", "application/x-www-form-urlencoded", form, headers, 200)
	if active["active"] != true {
		t.Fatal("authorized internal client could not inspect completion token")
	}
	p.stop()
	var ip, device string
	if err := f.pool.QueryRow(t.Context(), "SELECT context->>'client_ip', context->>'device_id' FROM audit_event WHERE context->>'request_id'='completion-final' AND action=$1", enum.EventSignIn.String()).Scan(&ip, &device); err != nil || ip != "127.0.0.1" || device != "completion-gateway-device" {
		t.Fatal("completion trusted a forged proxy chain or changed its device")
	}
	f.assertAuditPrivate()
	f.assertSafe(public.logs.String())
	f.assertSafe(internal.logs.String())
}

func serviceMigrationFlowKeys(t *testing.T) {
	f := newIntegration(t)
	f.localWeChat()
	f.env["ACCOUNTKIT_ENABLED_IDENTITY_KINDS"] = "EMAIL,WECHAT"
	f.env["ACCOUNTKIT_REQUIRED_IDENTITY_KINDS"] = "EMAIL"
	oldKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	f.secrets = append(f.secrets, oldKey, "private-old-key-code")
	p := f.start(map[string]string{"ACCOUNTKIT_SUBJECT_CIPHER_KEYS": "2:" + oldKey, "ACCOUNTKIT_SUBJECT_CIPHER_ACTIVE_KEY": "2"})
	_, pending := transportRequest(t, f.client, "POST", p.baseURL+"/v1/users:signInWithIdp", "application/json", providerSignInBody(t, "private-old-key-code"), map[string]string{"X-Device-Id": "old-key-device"}, 200)
	if token, _ := pending["sign_in_completion_token"].(string); token == "" {
		t.Fatal("missing old-key pending flow")
	} else {
		f.secrets = append(f.secrets, token)
	}
	p.stop()
	var payload []byte
	var version int
	if err := f.pool.QueryRow(t.Context(), "SELECT payload, cipher_version FROM identity_flow").Scan(&payload, &version); err != nil || version != 2 || len(payload) == 0 {
		t.Fatal("old-key flow was not encrypted and persisted")
	}
	minimal := map[string]string{"ACCOUNTSVC_SMTP_PORT": "invalid-unused", "ACCOUNTKIT_WECHAT_APPS": "", "ACCOUNTSVC_INTROSPECTION_CLIENTS": ""}
	if _, err := f.command(minimal, "migrate"); err == nil {
		t.Fatal("migrate ignored an active flow's missing cipher key")
	}
	var unchanged []byte
	if err := f.pool.QueryRow(t.Context(), "SELECT payload FROM identity_flow").Scan(&unchanged); err != nil || !bytes.Equal(unchanged, payload) {
		t.Fatal("failed key check mutated the encrypted flow")
	}
	minimal["ACCOUNTKIT_SUBJECT_CIPHER_KEYS"] = f.env["ACCOUNTKIT_SUBJECT_CIPHER_KEYS"] + ",2:" + oldKey
	if _, err := f.command(minimal, "migrate"); err != nil {
		t.Fatal("migrate rejected a retained old flow key")
	}
	if _, err := f.pool.Exec(t.Context(), "UPDATE identity_flow SET expire_time=now()-interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	delete(minimal, "ACCOUNTKIT_SUBJECT_CIPHER_KEYS")
	if _, err := f.command(minimal, "migrate"); err != nil {
		t.Fatal("expired flow still required its old key")
	}
}

func serviceMigrationLock(t *testing.T, kind string) {
	f := newIntegration(t)
	ctx := t.Context()
	holder, err := pgx.Connect(ctx, f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close(context.Background())
	var dbName string
	if err := holder.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	var key any
	if kind == "initialization" {
		sum := sha256.Sum256([]byte("accountkit:migration:init:" + dbName + "\x00" + f.schema))
		key = int64(binary.BigEndian.Uint64(sum[:8]))
	} else {
		key, err = database.GenerateAdvisoryLockId(dbName, f.schema, "schema_migrations")
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatal(err)
	}
	minimal := map[string]string{"ACCOUNTSVC_STARTUP_TIMEOUT": "400ms", "ACCOUNTSVC_SMTP_PORT": "invalid-unused", "ACCOUNTKIT_ENABLED_IDENTITY_KINDS": "PHONE,WECHAT", "ACCOUNTKIT_REQUIRED_IDENTITY_KINDS": "PHONE", "ACCOUNTKIT_WECHAT_APPS": ""}
	start := time.Now()
	if _, err := f.command(minimal, "migrate"); err == nil {
		t.Fatal("migrate ignored a held migration lock")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("migration lock timeout exceeded its startup budget")
	}
	var namespaces int
	if err := holder.QueryRow(ctx, "SELECT count(*) FROM pg_namespace WHERE nspname=$1", f.schema).Scan(&namespaces); err != nil || namespaces != 0 {
		t.Fatal("timed-out migration changed the schema before acquiring its lock")
	}
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_unlock($1)", key); err != nil {
		t.Fatal(err)
	}
	minimal["ACCOUNTSVC_STARTUP_TIMEOUT"] = "5s"
	if _, err := f.command(minimal, "migrate"); err != nil {
		t.Fatal("migration retry after lock timeout failed or leaked a lock")
	}
	var version int
	var dirty bool
	if err := f.pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 2 || dirty {
		t.Fatal("migration retry did not finish cleanly")
	}
}
