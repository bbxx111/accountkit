package enduser

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/user"
	"github.com/bbxx111/accountkit/user/code"
	"github.com/bbxx111/accountkit/user/idp"
)

func TestClientInfoResourceNestedAndCopied(t *testing.T) {
	in := &user.ClientInfo{ClientType: enum.ClientNative, DeviceManufacturer: "Example", AppBuild: "200", AppName: "App", BrowserName: "Chrome", Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`{"revision":9007199254740993}`)}}
	out := newClientInfoResource(in)
	in.AppName = "Changed"
	in.Extensions["engine"][12] = '1'
	in.Extensions["new"] = json.RawMessage(`true`)
	raw, err := json.Marshal(out)
	want := `{"type":"NATIVE","device":{"manufacturer":"Example"},"app":{"name":"App","build":"200"},"browser":{"name":"Chrome"},"extensions":{"engine":{"revision":9007199254740993}}}`
	if err != nil || string(raw) != want {
		t.Fatalf("DTO=%s err=%v", raw, err)
	}
}

// testHandler 直接构造 Handler（内部测试包可访问私有字段）；映射测试不调用领域方法，Users 留空。
func testHandler(t *testing.T) *Handler {
	t.Helper()
	return &Handler{d: Deps{
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		RequestID:    func(*http.Request) string { return "rid" },
		ClientIP:     func(*http.Request) string { return "ip" },
		Now:          time.Now,
		ReauthMaxAge: time.Minute,
		MaxBodyBytes: defaultMaxBodyBytes,
	}}
}

func TestWriteServiceErrorMapping(t *testing.T) {
	h := testHandler(t)
	cases := []struct {
		err    error
		status int
		reason string
		retry  string
	}{
		{&code.RateLimitedError{Dimension: "COOLDOWN", RetryAfter: 45 * time.Second}, 429, "RESOURCE_EXHAUSTED/COOLDOWN", "45"},
		{&code.RateLimitedError{Dimension: "IP_LIMIT", RetryAfter: 1500 * time.Millisecond}, 429, "RESOURCE_EXHAUSTED/IP_LIMIT", "2"},
		{&code.RateLimitedError{Dimension: "TARGET_LIMIT", RetryAfter: 0}, 429, "RESOURCE_EXHAUSTED/TARGET_LIMIT", "1"},
		{fmt.Errorf("wrap: %w", code.ErrInvalid), 400, "INVALID_ARGUMENT/CODE_INVALID", ""},
		{code.ErrExpired, 400, "INVALID_ARGUMENT/CODE_EXPIRED", ""},
		{user.ErrInvalidTarget, 400, "INVALID_ARGUMENT/INVALID_TARGET", ""},
		{fmt.Errorf("%w: device id required", user.ErrInvalidArgument), 400, "INVALID_ARGUMENT/INVALID_ARGUMENT", ""},
		{user.ErrReauthenticationRequired, 400, "FAILED_PRECONDITION/REAUTHENTICATION_REQUIRED", ""},
		{user.ErrInsufficientScope, 403, "PERMISSION_DENIED/INSUFFICIENT_SCOPE", ""},
		{user.ErrNotAnchor, 400, "FAILED_PRECONDITION/TARGET_NOT_ANCHOR", ""},
		{user.ErrUserFrozen, 403, "PERMISSION_DENIED/USER_FROZEN", ""},
		{user.ErrNotFound, 404, "NOT_FOUND/NOT_FOUND", ""},
		{user.ErrUnavailable, 503, "UNAVAILABLE/DEPENDENCY_UNAVAILABLE", "1"},
		{user.ErrInvalidToken, 401, "UNAUTHENTICATED/TOKEN_INVALID", ""},
		{user.ErrInvalidGrant, 500, "INTERNAL/", ""}, // 非 /token 路径不该出现 → 500
		{errors.New("pg down"), 500, "INTERNAL/", ""},
		{idp.ErrInvalidCredential, 400, "INVALID_ARGUMENT/IDP_CREDENTIAL_INVALID", ""},
		{fmt.Errorf("%w: wechat app", idp.ErrAppNotAllowed), 400, "INVALID_ARGUMENT/IDP_APP_NOT_ALLOWED", ""},
		{idp.ErrNonceReplayed, 400, "INVALID_ARGUMENT/IDP_NONCE_REPLAYED", ""},
		{idp.ErrUnavailable, 503, "UNAVAILABLE/IDP_UNAVAILABLE", "1"},
		{idp.ErrMisconfigured, 500, "INTERNAL/", ""},
		{user.ErrIdentityConflict, 409, "ALREADY_EXISTS/IDENTITY_ALREADY_BOUND", ""},
		{user.ErrIdentityKindLimit, 409, "ALREADY_EXISTS/IDENTITY_KIND_LIMIT", ""},
		{user.ErrLastAnchor, 400, "FAILED_PRECONDITION/LAST_ANCHOR_IDENTITY", ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", nil)
		h.writeServiceError(rec, req, c.err)
		got, body := aipErrorInternal(t, rec)
		if rec.Code != c.status || got != c.reason || rec.Header().Get("Retry-After") != c.retry {
			t.Errorf("%v: got %d %s retry=%q body=%v", c.err, rec.Code, got, rec.Header().Get("Retry-After"), body)
		}
		if body := rec.Body.String(); rec.Code == 500 && (strings.Contains(body, "pg down") || strings.Contains(body, "misconfigured")) {
			t.Errorf("500 must not leak: %s", body)
		}
		if rec.Code == 401 && rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("401 must carry WWW-Authenticate")
		}
		if errors.Is(c.err, user.ErrInvalidArgument) {
			if msg, _ := body["message"].(string); strings.Contains(msg, "user:") {
				t.Errorf("INVALID_ARGUMENT message must strip the domain sentinel prefix: %q", msg)
			} else if msg != "device id required" {
				t.Errorf("INVALID_ARGUMENT message = %q, want %q", msg, "device id required")
			}
		}
	}
}

func TestWriteServiceErrorInvalidArgumentEmptyDetail(t *testing.T) {
	h := testHandler(t)
	rec := httptest.NewRecorder()
	h.writeServiceError(rec, httptest.NewRequest("POST", "/x", nil), user.ErrInvalidArgument)
	_, body := aipErrorInternal(t, rec)
	if body["message"] != "invalid argument" {
		t.Fatalf("bare ErrInvalidArgument message = %v, want %q", body["message"], "invalid argument")
	}
}

func TestWriteOAuthErrorMapping(t *testing.T) {
	h := testHandler(t)
	cases := []struct {
		err    error
		status int
		code   string
		hasRsn bool
	}{
		{user.ErrInvalidGrant, 400, "invalid_grant", false},
		{fmt.Errorf("%w: expired", user.ErrInvalidGrant), 400, "invalid_grant", false},
		{user.ErrUserFrozen, 403, "invalid_grant", true},
		{user.ErrUnavailable, 503, "temporarily_unavailable", false},
		{errMalformedBody, 400, "invalid_request", false},
		{errors.New("boom"), 500, "server_error", false},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.writeOAuthError(rec, httptest.NewRequest("POST", "/token", nil), c.err)
		var b map[string]any
		decodeInternal(t, rec, &b)
		if rec.Code != c.status || b["error"] != c.code || (b["reason"] == "USER_FROZEN") != c.hasRsn || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%v: %d %v", c.err, rec.Code, b)
		}
	}
}

func TestDTOShapes(t *testing.T) {
	tr := newTokenResponse(user.TokenResult{AccessToken: "a", RefreshToken: "r", ExpiresIn: 900, RefreshExpiresIn: 2592000, Scope: "user", UserID: "u_1", IsNewUser: true})
	if tr.TokenType != "Bearer" || tr.RefreshToken != "r" || tr.ExpiresIn != 900 {
		t.Fatalf("%+v", tr)
	}
	if got := newTokenResponse(user.TokenResult{HintEmail: "a@b.co"}).HintEmail; got != "a@b.co" {
		t.Fatalf("HintEmail = %q, want %q", got, "a@b.co")
	}
	if b, err := json.Marshal(newTokenResponse(user.TokenResult{})); err != nil || strings.Contains(string(b), "hint_email") {
		t.Fatalf("empty HintEmail must be omitted: %s (err=%v)", b, err)
	}
	del := testNowInternal.Add(time.Hour)
	me := newMeResource(user.Me{ID: "u_1", State: enum.UserPendingDeletion, DisplayName: "白博", CreateTime: testNowInternal, DeleteTime: &del})
	if me.Name != "users/u_1" || me.State != "PENDING_DELETION" || me.DisplayName != "白博" || me.CreateTime != "2026-09-10T12:00:00Z" || me.DeleteTime == nil || *me.DeleteTime != "2026-09-10T13:00:00Z" || me.PurgeTime != nil {
		t.Fatalf("%+v", me)
	}
	s := newSessionResource("u_1", user.SessionInfo{ID: "s_1", DeviceID: "d", DeviceName: "n", CreateTime: testNowInternal, LastUsedTime: testNowInternal, IsCurrent: true})
	if s.Name != "users/u_1/sessions/s_1" || !s.IsCurrent || s.LastUsedTime != "2026-09-10T12:00:00Z" {
		t.Fatalf("%+v", s)
	}
	ir := newIdentityResource("u_1", user.IdentityInfo{ID: "i_1", Kind: enum.IdentityPhone, MaskedSubject: "+86 138****1234", CreateTime: testNowInternal})
	if ir.Name != "users/u_1/identities/i_1" || ir.Kind != "PHONE" || ir.MaskedSubject != "+86 138****1234" || ir.CreateTime != "2026-09-10T12:00:00Z" {
		t.Fatalf("%+v", ir)
	}
	if b, _ := json.Marshal(newIdentityResource("u_1", user.IdentityInfo{ID: "i_2", Kind: enum.IdentityWeChat})); !strings.Contains(string(b), `"masked_subject":""`) {
		t.Fatalf("provider identity keeps an empty masked_subject field: %s", b)
	}
}

func TestDeviceFromHeaders(t *testing.T) {
	ok := func(id, name string) (user.Device, string) {
		req := httptest.NewRequest("POST", "/x", nil)
		if id != "" {
			req.Header.Set("X-Device-Id", id)
		}
		if name != "" {
			req.Header.Set("X-Device-Name", name)
		}
		d, e := deviceFrom(req)
		if e != nil {
			return d, e.Reason
		}
		return d, ""
	}
	if d, r := ok("3fa85f64-5717-4562-b3fc-2c963f66afa6", "Pixel 9"); r != "" || d.ID != "3fa85f64-5717-4562-b3fc-2c963f66afa6" || d.Name != "Pixel 9" {
		t.Fatalf("%+v %s", d, r)
	}
	if _, r := ok("", "x"); r != "DEVICE_ID_INVALID" {
		t.Fatal(r)
	}
	if _, r := ok("has space", ""); r != "DEVICE_ID_INVALID" {
		t.Fatal(r)
	}
	if _, r := ok(strings.Repeat("a", 65), ""); r != "DEVICE_ID_INVALID" {
		t.Fatal(r)
	}
	if _, r := ok("abc", strings.Repeat("字", 65)); r != "DEVICE_NAME_INVALID" {
		t.Fatal(r)
	}
	if _, r := ok("abc", "bad\nname"); r != "DEVICE_NAME_INVALID" {
		t.Fatal(r)
	}
	if _, r := ok("abc", "\xff\xfe"); r != "DEVICE_NAME_INVALID" {
		t.Fatal(r)
	}
	if d, r := ok("abc", strings.Repeat("字", 64)); r != "" || len([]rune(d.Name)) != 64 {
		t.Fatal("64 runes must pass")
	}
	if d, r := ok("abc", "  trimmed  "); r != "" || d.Name != "trimmed" {
		t.Fatal("name must be trimmed")
	}
}

func decodeCredentialBody(t *testing.T, body string) credential {
	t.Helper()
	var c credential
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

const testCodeID = "0123456789abcdef0123456789abcdef"

func TestCredentialAnchor(t *testing.T) {
	c := decodeCredentialBody(t, `{"kind":"PHONE","credential":{"target":"+86138","code_id":"`+testCodeID+`","code":"123456"}}`)
	kind, cc, e := c.anchor()
	if e != nil || kind != enum.IdentityPhone || cc.Code != "123456" || cc.Target != "+86138" || !c.isAnchor() {
		t.Fatalf("%v %v %+v", e, kind, cc)
	}
	if kind, _, e := decodeCredentialBody(t, `{"kind":"EMAIL","credential":{"target":"a@b.co","code_id":"`+testCodeID+`","code":"1"}}`).anchor(); e != nil || kind != enum.IdentityEmail {
		t.Fatal("email", e)
	}
	for body, reason := range map[string]string{
		`{}`:                                  "CREDENTIAL_INCOMPLETE",
		`{"kind":"PHONE"}`:                    "CREDENTIAL_INCOMPLETE",
		`{"kind":"PHONE","credential":null}`:  "CREDENTIAL_INCOMPLETE",
		`{"credential":{"target":"x"}}`:       "CREDENTIAL_INCOMPLETE",
		`{"kind":"phone","credential":{}}`:    "INVALID_ARGUMENT",
		`{"kind":"WECHAT","credential":{}}`:   "CREDENTIAL_KIND_NOT_ALLOWED",
		`{"kind":"CORP_SSO","credential":{}}`: "CREDENTIAL_KIND_NOT_ALLOWED",
		`{"kind":"PHONE","credential":[]}`:    "CREDENTIAL_INCOMPLETE",
		`{"kind":"PHONE","credential":{"target":"+86138","code_id":"` + testCodeID + `"}}`:                  "CREDENTIAL_INCOMPLETE",
		`{"kind":"PHONE","credential":{"target":"+86138","code_id":"` + testCodeID + `","code":"1","x":1}}`: "CREDENTIAL_INCOMPLETE",
		`{"kind":"PHONE","credential":{"target":"+86138","code_id":"bad","code":"1"}}`:                      "INVALID_ARGUMENT",
	} {
		if _, _, e := decodeCredentialBody(t, body).anchor(); e == nil || e.Reason != reason {
			t.Fatalf("%s: got %v, want %s", body, e, reason)
		}
	}
}

func TestCredentialProvider(t *testing.T) {
	got, e := decodeCredentialBody(t, `{"kind":"WECHAT","credential":{"app_id":"wx1","code":"c1"}}`).provider()
	if e != nil || got.Kind != enum.IdentityWeChat || string(got.Credential) != `{"app_id":"wx1","code":"c1"}` {
		t.Fatalf("%+v %v", got, e)
	}
	got, e = decodeCredentialBody(t, `{"kind":"CORP_SSO","credential": {"id_token":"t","nonce":"n"} }`).provider()
	if e != nil || got.Kind != "CORP_SSO" || string(got.Credential) != `{"id_token":"t","nonce":"n"}` {
		t.Fatalf("%+v %v", got, e)
	}
	big := `{"kind":"GOOGLE","credential":{"id_token":"` + strings.Repeat("a", maxProviderCredentialBytes) + `","nonce":"n"}}`
	for body, reason := range map[string]string{
		`{}`:                                  "CREDENTIAL_INCOMPLETE",
		`{"kind":"GOOGLE"}`:                   "CREDENTIAL_INCOMPLETE",
		`{"kind":"GOOGLE","credential":null}`: "CREDENTIAL_INCOMPLETE",
		`{"kind":"GOOGLE","credential":"x"}`:  "CREDENTIAL_INCOMPLETE",
		`{"kind":"GOOGLE","credential":[]}`:   "CREDENTIAL_INCOMPLETE",
		big:                                   "CREDENTIAL_INCOMPLETE",
		`{"kind":"1BAD","credential":{}}`:     "INVALID_ARGUMENT",
		`{"kind":"EMAIL","credential":{}}`:    "CREDENTIAL_KIND_NOT_ALLOWED",
		`{"kind":"PHONE","credential":{}}`:    "CREDENTIAL_KIND_NOT_ALLOWED",
	} {
		if _, e := decodeCredentialBody(t, body).provider(); e == nil || e.Reason != reason {
			t.Fatalf("%.80s: got %v, want %s", body, e, reason)
		}
	}
}

func TestParseChannel(t *testing.T) {
	if k, e := parseChannel("PHONE"); e != nil || k != enum.IdentityPhone {
		t.Fatal("PHONE")
	}
	if k, e := parseChannel("EMAIL"); e != nil || k != enum.IdentityEmail {
		t.Fatal("EMAIL")
	}
	for _, bad := range []string{"", "phone", "WECHAT", "APPLE", "SMS"} {
		if _, e := parseChannel(bad); e == nil || e.Reason != "CHANNEL_INVALID" {
			t.Fatalf("%q must be CHANNEL_INVALID", bad)
		}
	}
}

var testNowInternal = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func aipErrorInternal(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any) {
	t.Helper()
	var b struct {
		Error map[string]any `json:"error"`
	}
	decodeInternal(t, rec, &b)
	if b.Error == nil {
		t.Fatalf("not AIP-193: %s", rec.Body.String())
	}
	reason, _ := b.Error["reason"].(string)
	return b.Error["status"].(string) + "/" + reason, b.Error
}

func decodeInternal(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode (%d): %s", rec.Code, rec.Body.String())
	}
}

// README 的第三方登录示例必须能通过信封解析，避免文档与实现漂移。
func TestDocumentedCredentialExamplesDecode(t *testing.T) {
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, `{"kind": "`) {
			continue
		}
		n++
		c := decodeCredentialBody(t, line)
		if _, e := c.provider(); e != nil {
			t.Fatalf("README example rejected: %s: %v", line, e)
		}
	}
	if n < 3 {
		t.Fatalf("README credential examples not found: %d", n)
	}
}
