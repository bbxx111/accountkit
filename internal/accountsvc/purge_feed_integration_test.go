package accountsvc_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type feedPage struct {
	PurgedUsers []struct {
		UserID    string `json:"user_id"`
		PurgeTime string `json:"purge_time"`
	} `json:"purged_users"`
	NextPageToken string `json:"next_page_token"`
}

func (f *integrationFixture) purgedUsers(p *serviceProcess, secret, token string, want int) feedPage {
	f.t.Helper()
	r, _ := http.NewRequest("GET", p.baseURL+"/v1/purgedUsers?page_token="+url.QueryEscape(token), nil)
	r.SetBasicAuth("ai-food", secret)
	resp, err := f.client.Do(r)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out feedPage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != want || resp.Header.Get("Cache-Control") != "no-store" {
		f.t.Fatalf("purgedUsers status=%d want %d", resp.StatusCode, want)
	}
	return out
}

func TestServiceIntegrationPurgedUserFeed(t *testing.T) {
	f := newIntegration(t)
	feedSecret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{6}, 32))
	f.secrets = append(f.secrets, feedSecret)
	p := f.start(map[string]string{
		"ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS": `{"ai-food":["` + feedSecret + `"]}`,
		"ACCOUNTSVC_DELETION_COOLING_PERIOD":  "1s",
		"ACCOUNTSVC_MAINTENANCE_INTERVAL":     "1s",
	})
	// 内省凭据不能读取已清理清单。
	f.purgedUsers(p, f.secrets[1], "", 401)
	start := f.purgedUsers(p, feedSecret, "", 200)
	if len(start.PurgedUsers) != 0 || start.NextPageToken == "" {
		t.Fatalf("initial feed: %+v", start)
	}
	tokens := f.login(p, "purge-feed@example.test")
	access := tokens["access_token"].(string)
	me := f.call("GET", p.baseURL+"/v1/users/me", access, nil, 200)
	name, _ := me["name"].(string)
	userID, ok := strings.CutPrefix(name, "users/")
	if !ok || userID == "" {
		t.Fatalf("unexpected resource name %q", name)
	}
	f.call("DELETE", p.baseURL+"/v1/users/me", access, nil, 200)
	// 等待冷静期结束、维护任务完成清理。
	var got feedPage
	deadline := time.Now().Add(20 * time.Second)
	for {
		got = f.purgedUsers(p, feedSecret, start.NextPageToken, 200)
		if len(got.PurgedUsers) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(got.PurgedUsers) != 1 || got.PurgedUsers[0].PurgeTime == "" || got.PurgedUsers[0].UserID != userID {
		t.Fatalf("feed after purge: %+v (user %s)", got, userID)
	}
	f.introspect(p, access, false)
	// 追平后游标稳定，重复拉取同一位置结果相同。
	after := f.purgedUsers(p, feedSecret, got.NextPageToken, 200)
	if len(after.PurgedUsers) != 0 || after.NextPageToken != got.NextPageToken {
		t.Fatalf("caught-up feed: %+v", after)
	}
	again := f.purgedUsers(p, feedSecret, start.NextPageToken, 200)
	if len(again.PurgedUsers) != 1 || again.PurgedUsers[0].UserID != got.PurgedUsers[0].UserID {
		t.Fatalf("re-read: %+v", again)
	}
	p.stop()
	f.assertAuditPrivate()
}

func TestServiceIntegrationUnifiedPurgedUserFeed(t *testing.T) {
	f := newIntegration(t)
	var down atomic.Bool
	sign := f.serviceTokenIssuer(&down)
	f.unifiedServiceClients()
	basic, bearer := serviceBasic("business", f.secrets[1]), "Bearer "+sign("business")
	p := f.start(map[string]string{"ACCOUNTSVC_DELETION_COOLING_PERIOD": "1s", "ACCOUNTSVC_MAINTENANCE_INTERVAL": "1s"})
	read := func(authorization, cursor string) feedPage {
		t.Helper()
		_, raw := f.serviceRequest(p, "/v1/purgedUsers?page_size=1&page_token="+url.QueryEscape(cursor), authorization, "", 200)
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var page feedPage
		if err = json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
		if page.NextPageToken == "" {
			t.Fatalf("empty cursor: %+v", page)
		}
		return page
	}
	start := read(basic, "")
	if len(start.PurgedUsers) != 0 {
		t.Fatalf("unexpected initial feed: %+v", start)
	}
	want := map[string]bool{}
	var accessTokens []string
	for _, email := range []string{"unified-purge-one@example.test", "unified-purge-two@example.test"} {
		tokens := f.login(p, email)
		access := tokens["access_token"].(string)
		accessTokens = append(accessTokens, access)
		want[tokens["user_id"].(string)] = true
		f.call("DELETE", p.baseURL+"/v1/users/me", access, nil, 200)
	}
	// Read one record at a time, alternating the two methods of the same identity.
	// Purge records become visible only after the existing anonymization transaction commits.
	seen := map[string]bool{}
	cursor := start.NextPageToken
	deadline := time.Now().Add(20 * time.Second)
	for len(seen) < 2 {
		authorization := basic
		if len(seen)%2 == 0 {
			authorization = bearer
		}
		page := read(authorization, cursor)
		if len(page.PurgedUsers) > 1 {
			t.Fatalf("page_size ignored: %+v", page)
		}
		for _, record := range page.PurgedUsers {
			if !want[record.UserID] || seen[record.UserID] || record.PurgeTime == "" {
				t.Fatalf("unexpected/duplicate purged record: %+v", record)
			}
			seen[record.UserID] = true
		}
		cursor = page.NextPageToken
		if time.Now().After(deadline) {
			t.Fatalf("purge feed incomplete: seen=%v want=%v", seen, want)
		}
		if len(page.PurgedUsers) == 0 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	caughtUp := read(basic, cursor)
	if len(caughtUp.PurgedUsers) != 0 || caughtUp.NextPageToken != cursor {
		t.Fatalf("caught-up cursor changed: %+v", caughtUp)
	}
	if again := read(bearer, start.NextPageToken); len(again.PurgedUsers) != 1 || !want[again.PurgedUsers[0].UserID] {
		t.Fatalf("replay page: %+v", again)
	}
	for _, access := range accessTokens {
		_, out := f.serviceRequest(p, "/v1/introspect", bearer, access, 200)
		if out["active"] != false {
			t.Fatalf("purged consumer session active: %v", out)
		}
	}
	p.stop()
	f.assertAuditPrivate()
}
