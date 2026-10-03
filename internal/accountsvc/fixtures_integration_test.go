package accountsvc_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bbxx111/accountkit"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *safeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

type deliveredMail struct{ target, code, body string }
type smtpFixture struct {
	ln       net.Listener
	messages chan deliveredMail
	entered  chan struct{}
	hold     atomic.Bool
	release  chan struct{}
	reject   atomic.Bool
	mu       sync.Mutex
	conns    map[net.Conn]bool
	wg       sync.WaitGroup
}

func newSMTP(t *testing.T, cert tls.Certificate) *smtpFixture {
	t.Helper()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpFixture{ln: ln, messages: make(chan deliveredMail, 32), entered: make(chan struct{}, 8), release: make(chan struct{}), conns: map[net.Conn]bool{}}
	s.wg.Go(func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns[c] = true
			s.mu.Unlock()
			s.wg.Go(func() { defer c.Close(); defer func() { s.mu.Lock(); delete(s.conns, c); s.mu.Unlock() }(); s.serve(c) })
		}
	})
	t.Cleanup(func() {
		ln.Close()
		close(s.release)
		s.mu.Lock()
		for c := range s.conns {
			c.Close()
		}
		s.mu.Unlock()
		s.wg.Wait()
	})
	return s
}

func (s *smtpFixture) serve(c net.Conn) {
	_ = c.SetDeadline(time.Now().Add(40 * time.Second))
	rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))
	tr := textproto.NewReader(rw.Reader)
	send := func(line string) { fmt.Fprint(rw, line+"\r\n"); _ = rw.Flush() }
	send("220 test ESMTP")
	authenticated := false
	target := ""
	for {
		line, err := tr.ReadLine()
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO"):
			send("250-test\r\n250 AUTH PLAIN")
		case strings.HasPrefix(line, "AUTH PLAIN "):
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "AUTH PLAIN "))
			authenticated = err == nil && string(raw) == "\x00fixture-user\x00fixture-smtp-password"
			if authenticated {
				send("235 authenticated")
			} else {
				send("535 denied")
			}
		case strings.HasPrefix(line, "MAIL FROM:"):
			if !authenticated {
				send("530 authentication required")
			} else {
				send("250 ok")
			}
		case strings.HasPrefix(line, "RCPT TO:"):
			target = strings.TrimSuffix(strings.TrimPrefix(line, "RCPT TO:<"), ">")
			send("250 ok")
		case line == "DATA":
			send("354 send message")
			raw, err := io.ReadAll(tr.DotReader())
			if err != nil {
				return
			}
			msg, err := mail.ReadMessage(bytes.NewReader(raw))
			if err != nil {
				return
			}
			body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
			if err != nil {
				return
			}
			code := regexp.MustCompile(`[0-9]{6}`).FindString(string(body))
			s.messages <- deliveredMail{target, code, string(body)}
			if s.hold.Load() {
				s.entered <- struct{}{}
				<-s.release
			}
			if s.reject.Load() {
				send("550 fixture-smtp-password private recipient rejected")
			} else {
				send("250 queued")
			}
		case line == "QUIT":
			send("221 bye")
			return
		default:
			send("250 ok")
		}
	}
}

func (s *smtpFixture) mail(t *testing.T) deliveredMail {
	t.Helper()
	select {
	case m := <-s.messages:
		if len(m.code) != 6 {
			t.Fatal("SMTP message missing code")
		}
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("SMTP delivery missing")
		return deliveredMail{}
	}
}

type redisProxy struct {
	ln     net.Listener
	target string
	down   atomic.Bool
	mu     sync.Mutex
	conns  map[net.Conn]bool
	wg     sync.WaitGroup
}

func newRedisProxy(t *testing.T, rawURL string) *redisProxy {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "redis" {
		t.Fatal("integration proxy requires redis:// test URL")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &redisProxy{ln: ln, target: u.Host, conns: map[net.Conn]bool{}}
	p.wg.Go(func() {
		for {
			front, err := ln.Accept()
			if err != nil {
				return
			}
			if p.down.Load() {
				front.Close()
				continue
			}
			back, err := net.DialTimeout("tcp", p.target, time.Second)
			if err != nil {
				front.Close()
				continue
			}
			p.mu.Lock()
			if p.down.Load() {
				p.mu.Unlock()
				front.Close()
				back.Close()
				continue
			}
			p.conns[front] = true
			p.conns[back] = true
			p.mu.Unlock()
			p.wg.Go(func() {
				defer func() {
					front.Close()
					back.Close()
					p.mu.Lock()
					delete(p.conns, front)
					delete(p.conns, back)
					p.mu.Unlock()
				}()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(back, front); back.Close(); close(done) }()
				_, _ = io.Copy(front, back)
				front.Close()
				<-done
			})
		}
	})
	t.Cleanup(func() { ln.Close(); p.fail(true); p.wg.Wait() })
	return p
}
func (p *redisProxy) fail(down bool) {
	p.down.Store(down)
	if down {
		p.mu.Lock()
		for c := range p.conns {
			c.Close()
		}
		p.mu.Unlock()
	}
}

type integrationFixture struct {
	t                                          *testing.T
	binary, dir, schema, prefix, dsn, redisURL string
	env                                        map[string]string
	pool                                       *pgxpool.Pool
	redis                                      *redis.Client
	client                                     *http.Client
	smtp                                       *smtpFixture
	cert                                       tls.Certificate
	secrets                                    []string
}

func requireServiceIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("SERVER_TEST_DB_DSN") == "" || os.Getenv("ACCOUNTSVC_TEST_REDIS_URL") == "" {
		t.Skip("real service integration requires SERVER_TEST_DB_DSN and ACCOUNTSVC_TEST_REDIS_URL")
	}
	if runtime.GOOS != "linux" {
		t.Skip("service TLS trust and actual SIGTERM verification require Linux")
	}
}

func newIntegration(t *testing.T) *integrationFixture {
	t.Helper()
	requireServiceIntegration(t)
	dsn, redisURL := os.Getenv("SERVER_TEST_DB_DSN"), os.Getenv("ACCOUNTSVC_TEST_REDIS_URL")
	dir := t.TempDir()
	binary := filepath.Join(dir, "accountsvc")
	args := []string{"build", "-o", binary}
	if os.Getenv("ACCOUNTSVC_TEST_RACE") == "1" {
		args = append(args, "-race")
	}
	args = append(args, "./cmd/accountsvc")
	build := exec.Command("go", args...)
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build accountsvc: %v\n%s", err, out)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("acctsvc_it_%x", suffix)
	prefix := schema + ":"
	pc, err := accountkit.PoolConfig(dsn, schema)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), pc)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal("real PostgreSQL unavailable")
	}
	ro, err := redis.ParseURL(redisURL)
	if err != nil {
		pool.Close()
		t.Fatal(err)
	}
	rc := redis.NewClient(ro)
	if err := rc.Ping(ctx).Err(); err != nil {
		rc.Close()
		pool.Close()
		t.Fatal("real Redis unavailable")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := pool.Exec(ctx, "DROP SCHEMA IF EXISTS "+schema+" CASCADE")
		if err != nil {
			t.Error("cleanup owned schema failed")
		}
		iter := rc.Scan(ctx, 0, prefix+"*", 100).Iterator()
		for iter.Next(ctx) {
			if err := rc.Del(ctx, iter.Val()).Err(); err != nil {
				t.Error("cleanup owned Redis key failed")
			}
		}
		if err := iter.Err(); err != nil {
			t.Error("cleanup owned Redis scan failed")
		}
		rc.Close()
		pool.Close()
	})
	cert, certPEM, keyPEM := integrationCertificate(t)
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(certPEM)
	client := &http.Client{Timeout: 6 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
	t.Cleanup(func() { client.CloseIdleConnections() })
	smtp := newSMTP(t, cert)
	_, port, _ := net.SplitHostPort(smtp.ln.Addr().String())
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	clientSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	f := &integrationFixture{t: t, binary: binary, dir: dir, schema: schema, prefix: prefix, dsn: dsn, redisURL: redisURL, pool: pool, redis: rc, client: client, smtp: smtp, cert: cert, secrets: []string{"fixture-smtp-password", clientSecret, key}}
	f.env = map[string]string{"SSL_CERT_FILE": certFile, "ACCOUNTSVC_MODE": "production", "ACCOUNTSVC_DATABASE_URL": dsn, "ACCOUNTSVC_REDIS_URL": redisURL, "ACCOUNTSVC_TLS_CERT_FILE": certFile, "ACCOUNTSVC_TLS_KEY_FILE": keyFile, "ACCOUNTSVC_SMTP_HOST": "127.0.0.1", "ACCOUNTSVC_SMTP_PORT": port, "ACCOUNTSVC_SMTP_FROM": "account@example.test", "ACCOUNTSVC_SMTP_TLS_MODE": "implicit", "ACCOUNTSVC_SMTP_USERNAME": "fixture-user", "ACCOUNTSVC_SMTP_PASSWORD": "fixture-smtp-password", "ACCOUNTSVC_SMTP_TIMEOUT": "30s", "ACCOUNTSVC_INTROSPECTION_CLIENTS": `{"business":["` + clientSecret + `"]}`, "ACCOUNTSVC_STARTUP_TIMEOUT": "10s", "ACCOUNTSVC_SHUTDOWN_TIMEOUT": "18s", "ACCOUNTKIT_AUTH_SCHEMA": schema, "ACCOUNTKIT_AUTH_KEY_PREFIX": prefix, "ACCOUNTKIT_JWT_KEYS": "1:" + key, "ACCOUNTKIT_JWT_ACTIVE_KEY": "1", "ACCOUNTKIT_JWT_ISSUER": "integration-consumer", "ACCOUNTKIT_JWT_AUDIENCE": "integration-business", "ACCOUNTKIT_SUBJECT_HMAC_KEYS": "1:" + key, "ACCOUNTKIT_SUBJECT_HMAC_ACTIVE_KEY": "1", "ACCOUNTKIT_SUBJECT_CIPHER_KEYS": "1:" + key, "ACCOUNTKIT_SUBJECT_CIPHER_ACTIVE_KEY": "1", "ACCOUNTKIT_CODE_COOLDOWN": "1s", "ACCOUNTKIT_MAINTENANCE_INTERVAL": "1h"}
	// Validate actual library environment parsing before any service process can
	// migrate or send a request. A misspelled key must never fall back to auth.
	for name, value := range f.env {
		if strings.HasPrefix(name, "ACCOUNTKIT_") {
			t.Setenv(name, value)
		}
	}
	parsed, err := accountkit.ConfigFromEnv("ACCOUNTKIT_")
	if err != nil || parsed.Schema != schema || parsed.KeyPrefix != prefix {
		t.Fatal("integration fixture did not configure isolated schema and Redis prefix")
	}
	return f
}

func integrationCertificate(t *testing.T) (tls.Certificate, []byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "accountsvc integration fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cp, kp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
	cert, err := tls.X509KeyPair(cp, kp)
	if err != nil {
		t.Fatal(err)
	}
	return cert, cp, kp
}

func freeAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}
func (f *integrationFixture) environment(extra map[string]string) []string {
	values := map[string]string{}
	for _, line := range os.Environ() {
		k, v, _ := strings.Cut(line, "=")
		if !strings.HasPrefix(k, "ACCOUNTKIT_") && !strings.HasPrefix(k, "ACCOUNTSVC_") && k != "SSL_CERT_FILE" {
			values[k] = v
		}
	}
	for k, v := range f.env {
		values[k] = v
	}
	for k, v := range extra {
		if v == "" {
			delete(values, k)
		} else {
			values[k] = v
		}
	}
	out := make([]string, 0, len(values))
	for k, v := range values {
		out = append(out, k+"="+v)
	}
	return out
}

type serviceProcess struct {
	fixture          *integrationFixture
	cmd              *exec.Cmd
	output           safeBuffer
	done             chan struct{}
	err              error
	public, internal string
	stopped          bool
}

func (f *integrationFixture) start(extra map[string]string) *serviceProcess {
	f.t.Helper()
	public, internal := freeAddress(f.t), freeAddress(f.t)
	env := map[string]string{"ACCOUNTSVC_HTTP_ADDR": public, "ACCOUNTSVC_INTERNAL_ADDR": internal}
	for k, v := range extra {
		env[k] = v
	}
	p := &serviceProcess{fixture: f, done: make(chan struct{}), public: "https://" + public, internal: "https://" + internal}
	p.cmd = exec.Command(f.binary)
	p.cmd.Env = f.environment(env)
	p.cmd.Stdout = &p.output
	p.cmd.Stderr = &p.output
	if err := p.cmd.Start(); err != nil {
		f.t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	f.t.Cleanup(func() {
		if !p.stopped {
			p.cmd.Process.Kill()
			<-p.done
		}
		f.assertSafe(p.output.String())
	})
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			f.assertSafe(p.output.String())
			f.t.Fatalf("service failed startup: %v %s", p.err, p.output.String())
		default:
		}
		resp, err := f.client.Get(p.internal + "/readyz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return p
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.assertSafe(p.output.String())
	f.t.Fatalf("service not ready: %s", p.output.String())
	return nil
}
func (p *serviceProcess) signal() {
	p.fixture.t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		p.fixture.t.Fatal(err)
	}
}
func (p *serviceProcess) wait(wantSuccess bool, max time.Duration) {
	p.fixture.t.Helper()
	select {
	case <-p.done:
		p.stopped = true
		p.fixture.assertSafe(p.output.String())
		if (p.err == nil) != wantSuccess {
			p.fixture.t.Fatalf("exit=%v want success=%v output=%s", p.err, wantSuccess, p.output.String())
		}
	case <-time.After(max):
		p.fixture.t.Fatal("process shutdown exceeded test bound")
	}
	p.fixture.assertSafe(p.output.String())
}
func (p *serviceProcess) stop() { p.signal(); p.wait(true, 20*time.Second) }
func (f *integrationFixture) assertSafe(output string) {
	f.t.Helper()
	for _, secret := range f.secrets {
		if secret != "" && strings.Contains(output, secret) {
			f.t.Fatal("process output exposed a test credential or private identity")
		}
	}
}

func (f *integrationFixture) call(method, address, bearer string, body any, want int) map[string]any {
	f.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	r, err := http.NewRequest(method, address, rd)
	if err != nil {
		f.t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Device-Id", "integration-device")
	r.Header.Set("X-Request-Id", "svc-integration")
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := f.client.Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != want {
		f.t.Fatalf("%s expected %d got %d", r.URL.Path, want, resp.StatusCode)
	}
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}
func (f *integrationFixture) login(p *serviceProcess, target string) map[string]any {
	f.t.Helper()
	f.call("POST", p.public+"/v1/users:sendSignInCode", "", map[string]string{"channel": "EMAIL", "target": target}, 200)
	m := f.smtp.mail(f.t)
	if m.target != target || !strings.Contains(m.body, "登录") {
		f.t.Fatal("wrong SMTP recipient or purpose")
	}
	f.secrets = append(f.secrets, target, m.code)
	out := f.call("POST", p.public+"/v1/users:signInWithCode", "", map[string]any{"email": map[string]string{"target": target, "code": m.code}}, 200)
	f.secrets = append(f.secrets, out["access_token"].(string), out["refresh_token"].(string))
	return out
}
func (f *integrationFixture) introspect(p *serviceProcess, token string, active bool) {
	f.t.Helper()
	r, _ := http.NewRequest("POST", p.internal+"/internal/v1/introspect", strings.NewReader(url.Values{"token": {token}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.SetBasicAuth("business", f.secrets[1])
	resp, err := f.client.Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		f.t.Fatal(err)
	}
	if resp.StatusCode != 200 || out["active"] != active || resp.Header.Get("Cache-Control") != "no-store" {
		f.t.Fatalf("introspection status=%d active=%v", resp.StatusCode, out["active"])
	}
	if !active && len(out) != 1 {
		f.t.Fatal("inactive response exposed details")
	}
}

func (f *integrationFixture) oidc() *httptest.Server {
	f.t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		f.t.Fatal(err)
	}
	var s *httptest.Server
	s = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/.well-known/openid-configuration" {
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": s.URL, "jwks_uri": s.URL + "/keys"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "fixture", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	s.TLS = &tls.Config{Certificates: []tls.Certificate{f.cert}, MinVersion: tls.VersionTLS12}
	s.StartTLS()
	f.t.Cleanup(s.Close)
	f.env["ACCOUNTSVC_ADMIN_ENABLED"] = "true"
	f.env["ACCOUNTSVC_ADMIN_ISSUER"] = s.URL
	f.env["ACCOUNTSVC_ADMIN_AUDIENCE"] = "integration-admin"
	for _, role := range []string{"operator", "super-admin"} {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": s.URL, "aud": "integration-admin", "sub": "admin-" + role, "exp": time.Now().Add(time.Hour).Unix(), "roles": []string{role}})
		token.Header["kid"] = "fixture"
		raw, err := token.SignedString(key)
		if err != nil {
			f.t.Fatal(err)
		}
		f.env["FIXTURE_"+role] = raw
		f.secrets = append(f.secrets, raw)
	}
	return s
}
