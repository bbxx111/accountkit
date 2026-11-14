package accountsvc_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestServiceIntegrationIdentityResolution(t *testing.T) {
	f := newIntegration(t)
	p := f.start(map[string]string{"ACCOUNTSVC_SERVICE_CLIENT_BUSINESS_PERMISSIONS": "identities.resolve"})
	resolve := func(subject string, want int) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"kind": "EMAIL", "subject": subject})
		headers, out := transportRequest(t, f.client, "POST", p.baseURL+"/v1/identities:resolve", "application/json", string(raw), map[string]string{"Authorization": serviceBasic("business", f.secrets[1])}, want)
		if headers.Get("Cache-Control") != "no-store" {
			t.Fatal(headers)
		}
		return out
	}
	resolve("missing@example.test", 404)
	tokens := f.login(p, "resolve@example.test")
	access := tokens["access_token"].(string)
	me := f.call("GET", p.baseURL+"/v1/users/me", access, nil, 200)
	id := strings.TrimPrefix(me["name"].(string), "users/")
	if _, err := f.pool.Exec(t.Context(), `UPDATE "`+f.schema+`".user_account SET display_name=$1, avatar_url=$2 WHERE id=$3`, "Resolve synthetic", "https://example.test/avatar.png", id); err != nil {
		t.Fatal(err)
	}
	out := resolve("RESOLVE@example.test", 200)
	if len(out) != 4 || out["user_id"] != id || out["state"] != "ACTIVE" || out["display_name"] != "Resolve synthetic" || out["avatar_url"] != "https://example.test/avatar.png" {
		t.Fatal(out)
	}
	for _, state := range []struct {
		value int
		name  string
	}{{2, "FROZEN"}, {3, "PENDING_DELETION"}} {
		if _, err := f.pool.Exec(t.Context(), `UPDATE "`+f.schema+`".user_account SET state=$1 WHERE id=$2`, state.value, id); err != nil {
			t.Fatal(err)
		}
		if out := resolve("resolve@example.test", 200); out["state"] != state.name || out["display_name"] != "Resolve synthetic" {
			t.Fatal(out)
		}
	}
	f.serviceRequest(p, "/v1/introspect", serviceBasic("business", f.secrets[1]), "", 503)
	f.serviceRequest(p, "/v1/purgedUsers", serviceBasic("business", f.secrets[1]), "", 503)
	transportRequest(t, f.client, "POST", p.baseURL+"/v1/identities:resolve", "application/json", `{}`, map[string]string{"Authorization": "Bearer " + access}, http.StatusUnauthorized)
}
