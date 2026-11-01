//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRealHostRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	h, e := start(ctx, options{database: os.Getenv("SDK_FIXTURE_DATABASE_URL"), redis: os.Getenv("SDK_FIXTURE_REDIS_ADDR"), addr: "127.0.0.1:0"})
	if e != nil {
		t.Fatalf("required real fixture startup: %v", e)
	}
	defer h.close()
	for _, path := range []string{"/__fixture/ready", "/keys", "/__fixture/admin-token?role=operator", "/__fixture/admin-token?role=super-admin"} {
		resp, e := http.Get(h.origin + path)
		if e != nil {
			t.Fatal(e)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s returned %d", path, resp.StatusCode)
		}
	}
	for _, kind := range []string{"APPLE", "GOOGLE", "FACEBOOK", "TEST_OIDC", "WECHAT", "WECHAT_MINI", "QQ", "WEIBO", "ALIPAY", "FACEBOOK_CLASSIC"} {
		resp, e := http.Get(h.origin + "/__fixture/credential?kind=" + kind + "&subject=123456789&nonce=unique-" + kind)
		if e != nil {
			t.Fatal(e)
		}
		var body struct {
			Kind       string          `json:"kind"`
			Credential json.RawMessage `json:"credential"`
		}
		e = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if e != nil || resp.StatusCode != 200 || body.Kind == "" || len(body.Credential) == 0 {
			t.Fatalf("credential %s invalid (%d)", kind, resp.StatusCode)
		}
	}
}

func TestAccountsvcRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h, e := start(ctx, options{database: os.Getenv("SDK_FIXTURE_DATABASE_URL"), redis: os.Getenv("SDK_FIXTURE_REDIS_ADDR"), addr: "127.0.0.1:0", profile: "accountsvc"})
	if e != nil {
		t.Fatalf("required accountsvc startup: %v", e)
	}
	defer h.close()
	resp, e := http.Get(h.origin + "/__fixture/ready")
	if e != nil {
		t.Fatal(e)
	}
	var meta struct {
		Origin string `json:"origin"`
	}
	e = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	if e != nil || meta.Origin == "" || meta.Origin == h.origin {
		t.Fatal("accountsvc origin was not separated from embedded control host")
	}
	resp, e = http.Get(meta.Origin + "/readyz")
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("accountsvc not ready: %d", resp.StatusCode)
	}
}

// This is a fixture transport smoke check, not an SDK contract test.
func TestAccountsvcSMTPRequired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	h, e := start(ctx, options{database: os.Getenv("SDK_FIXTURE_DATABASE_URL"), redis: os.Getenv("SDK_FIXTURE_REDIS_ADDR"), addr: "127.0.0.1:0", profile: "accountsvc"})
	if e != nil {
		t.Fatal(e)
	}
	defer h.close()
	resp, e := http.Get(h.origin + "/__fixture/ready")
	if e != nil {
		t.Fatal(e)
	}
	var meta struct {
		Origin string `json:"origin"`
	}
	json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	target := fmt.Sprintf("fixture-%d@example.test", time.Now().UnixNano())
	resp, e = http.Post(meta.Origin+"/v1/users:sendSignInCode", "application/json", strings.NewReader(`{"channel":"EMAIL","target":"`+target+`"}`))
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("send fixture mail: %d", resp.StatusCode)
	}
	resp, e = http.Get(h.origin + "/__fixture/code?target=" + target + "&purpose=SIGN_IN")
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var result struct {
		Code string `json:"code"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != 200 || len(result.Code) != 6 {
		t.Fatalf("SMTP capture unavailable (%d)", resp.StatusCode)
	}
}
