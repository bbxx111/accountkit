package enduser_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/enum"
	"github.com/bbxx111/accountkit/httpapi/enduser"
	"github.com/bbxx111/accountkit/user"
)

// Detects a documented response that rejects the actual rich/legacy current-session payload.
func TestCurrentSessionOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile("../../docs/openapi/enduser.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	for _, rich := range []bool{false, true} {
		t.Run(map[bool]string{false: "without description", true: "with description"}[rich], func(t *testing.T) {
			svc := &currentSessionService{fakeService: withAuth(&fakeService{}, principal), current: func(context.Context, user.Principal) (user.CurrentSessionInfo, error) {
				out := user.CurrentSessionInfo{SessionInfo: user.SessionInfo{ID: principal.SessionID, DeviceID: "browser", CreateTime: testNow, LastUsedTime: testNow}, AuthTime: testNow}
				out.AuthenticationIdentities = []user.AuthenticationIdentity{{ID: "i_0k3f9c2m1xq8z", Kind: enum.IdentityEmail}}
				if rich {
					out.AuthenticationIdentities = []user.AuthenticationIdentity{{ID: "i_0k3f9c2m1xq8z", Kind: enum.IdentityEmail}}
					out.ClientInfo = &user.ClientInfo{ClientType: enum.ClientNative, AppName: "Example App", AppVersion: "2.0", OSName: "Android", OSVersion: "15", DeviceType: enum.DevicePhone, DeviceModel: "Example Phone"}
					out.ClientInfoUpdateTime = &testNow
				}
				return out, nil
			}}
			h, err := enduser.New(enduser.Deps{Users: svc, ReauthMaxAge: time.Minute, ClientIP: func(*http.Request) string { return "203.0.113.1" }, RequestID: func(*http.Request) string { return "contract" }})
			if err != nil {
				t.Fatal(err)
			}
			rec := do(t, h.Router(), call{method: "GET", path: "/users/me/sessions/current", bearer: "good"})
			if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %v", rec.Code, rec.Header())
			}
			var payload any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			schema, ok := schemas["CurrentSession"].(map[string]any)
			if !ok {
				t.Fatal("OpenAPI missing CurrentSession response schema")
			}
			checkSessionSchema(t, schemas, schema, payload, "current session")
		})
	}
}

// This test-only validator supports the explicit object/array/scalar subset used by
// these DTOs. Full OpenAPI validation remains scripts/check-openapi.cjs + Swagger Parser.
func checkSessionSchema(t *testing.T, schemas map[string]any, schema map[string]any, value any, location string) {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		schema = schemas[ref[len("#/components/schemas/"):]].(map[string]any)
	}
	switch schema["type"] {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("%s not object", location)
		}
		props := schema["properties"].(map[string]any)
		if required, ok := schema["required"].([]any); ok {
			for _, key := range required {
				if _, ok := object[key.(string)]; !ok {
					t.Fatalf("%s missing %s", location, key)
				}
			}
		}
		for key, v := range object {
			prop, ok := props[key].(map[string]any)
			if !ok {
				t.Fatalf("%s undocumented property %s", location, key)
			}
			checkSessionSchema(t, schemas, prop, v, location+"."+key)
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			t.Fatalf("%s not array", location)
		}
		for _, item := range items {
			checkSessionSchema(t, schemas, schema["items"].(map[string]any), item, location+"[]")
		}
	case "string":
		str, ok := value.(string)
		if !ok {
			t.Fatalf("%s not string", location)
		}
		if schema["format"] == "date-time" {
			if _, err := time.Parse(time.RFC3339, str); err != nil {
				t.Fatalf("%s: %v", location, err)
			}
		}
		if pattern, ok := schema["pattern"].(string); ok && !regexp.MustCompile(pattern).MatchString(str) {
			t.Fatalf("%s pattern mismatch", location)
		}
		if values, ok := schema["enum"].([]any); ok {
			found := false
			for _, v := range values {
				found = found || v == str
			}
			if !found {
				t.Fatalf("%s unknown enum", location)
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			t.Fatalf("%s not boolean", location)
		}
	default:
		t.Fatalf("unsupported contract schema at %s: %v", location, schema)
	}
	if constant, ok := schema["const"]; ok && constant != value {
		t.Fatalf("%s const mismatch", location)
	}
}
