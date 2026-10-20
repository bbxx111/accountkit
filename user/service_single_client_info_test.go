package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/user"
)

func TestSessionClientInfoReplacesWholeObservation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pair := clientLogin(t, f, &user.ClientInfo{OSName: "Linux", AppName: "Example", AppVersion: "1"})
	p := principalOf(t, f, pair)
	f.advance(time.Minute)
	next, err := f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{BrowserName: "WebView"}))
	if err != nil {
		t.Fatal(err)
	}
	current, err := f.svc.GetCurrentSession(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if current.ClientInfo == nil || current.ClientInfo.BrowserName != "WebView" || current.ClientInfo.OSName != "" || current.ClientInfo.AppName != "" {
		t.Fatalf("new observation retained stale fields: %+v", current.ClientInfo)
	}
	var count int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM session_client_info WHERE session_id=$1`, p.SessionID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("one record per session: %d %v", count, err)
	}
	before, at := current.ClientInfo, *current.ClientInfoUpdateTime
	f.advance(time.Minute)
	if _, err = f.svc.Refresh(ctx, next.RefreshToken, clientMeta(nil)); err != nil {
		t.Fatal(err)
	}
	current, err = f.svc.GetCurrentSession(ctx, p)
	if err != nil || !reflect.DeepEqual(current.ClientInfo, before) || !current.ClientInfoUpdateTime.Equal(at) {
		t.Fatalf("missing info overwrote observation: %+v %v", current, err)
	}
}

func TestStoredClientInfoInvalidJSONIsInternal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := principalOf(t, f, clientLogin(t, f, &user.ClientInfo{OSName: "Linux"}))
	for _, content := range []string{`[]`, `null`, `{}`, `{"os":{"name":42}}`, `{"extensions":{"access_token":"synthetic-secret"}}`} {
		if _, err := f.pool.Exec(ctx, `UPDATE session_client_info SET client_info=$2 WHERE session_id=$1`, p.SessionID, content); err != nil {
			t.Fatal(err)
		}
		_, detailErr := f.svc.GetCurrentSession(ctx, p)
		_, listErr := f.svc.ListSessions(ctx, p.UserID, p.SessionID)
		for _, err := range []error{detailErr, listErr} {
			if err == nil || errors.Is(err, user.ErrInvalidClientInfo) || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("stored corruption exposed or classified as input: %v", err)
			}
		}
	}
}

func TestClientInfoNumbersSurviveJSONBRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	info := &user.ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(`1e1900`), "precise": json.RawMessage(`9007199254740993`), "fraction": json.RawMessage(`-1.25e-2`)}}
	pair := clientLogin(t, f, info)
	p := principalOf(t, f, pair)
	current, err := f.svc.GetCurrentSession(ctx, p)
	if err != nil {
		t.Fatal("valid numeric JSONB not readable", err)
	}
	if got := string(current.ClientInfo.Extensions["engine"]); got != "1"+strings.Repeat("0", 1900) {
		t.Fatal("exponent changed value")
	}
	if string(current.ClientInfo.Extensions["precise"]) != "9007199254740993" || string(current.ClientInfo.Extensions["fraction"]) != "-0.0125" {
		t.Fatal("number precision lost")
	}
	before := current.ClientInfo
	for _, number := range []string{"1e3000", "1e-3000", "1e999999999999999999999"} {
		_, err = f.svc.Refresh(ctx, pair.RefreshToken, clientMeta(&user.ClientInfo{Extensions: map[string]json.RawMessage{"engine": json.RawMessage(number)}}))
		if !errors.Is(err, user.ErrInvalidClientInfo) {
			t.Fatalf("unsafe expansion not rejected before rotation: %v", err)
		}
		got, readErr := f.svc.GetCurrentSession(ctx, p)
		if readErr != nil || !reflect.DeepEqual(got.ClientInfo, before) {
			t.Fatal("rejected number changed record", readErr)
		}
	}
	if _, err = f.svc.Refresh(ctx, pair.RefreshToken, meta1); err != nil {
		t.Fatal("invalid numeric observation consumed refresh", err)
	}
}
