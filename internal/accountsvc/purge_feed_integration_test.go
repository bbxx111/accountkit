package accountsvc_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
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
