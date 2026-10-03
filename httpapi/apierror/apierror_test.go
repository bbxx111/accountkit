package apierror_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/httpapi/apierror"
)

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[string]int{
		apierror.StatusInvalidArgument: 400, apierror.StatusFailedPrecondition: 400,
		apierror.StatusUnauthenticated: 401, apierror.StatusPermissionDenied: 403,
		apierror.StatusNotFound: 404, apierror.StatusResourceExhausted: 429,
		apierror.StatusInternal: 500, apierror.StatusUnavailable: 503, "BOGUS": 500,
		apierror.StatusAlreadyExists: 409,
	}
	for s, want := range cases {
		if got := apierror.HTTPStatus(s); got != want {
			t.Errorf("%s: got %d want %d", s, got, want)
		}
	}
}

func TestWriteAIP193Body(t *testing.T) {
	rec := httptest.NewRecorder()
	apierror.Write(rec, &apierror.Error{Status: apierror.StatusResourceExhausted, Reason: "COOLDOWN", Message: "try later", RetryAfterSeconds: 60})
	if rec.Code != 429 || rec.Header().Get("Content-Type") != "application/json; charset=utf-8" || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("status/headers: %d %v", rec.Code, rec.Header())
	}
	var body struct {
		Error struct {
			Code              int    `json:"code"`
			Message           string `json:"message"`
			Status            string `json:"status"`
			Reason            string `json:"reason"`
			RetryAfterSeconds int    `json:"retry_after_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != 429 || body.Error.Status != "RESOURCE_EXHAUSTED" || body.Error.Reason != "COOLDOWN" || body.Error.Message != "try later" || body.Error.RetryAfterSeconds != 60 {
		t.Fatalf("body: %+v", body)
	}
}

func TestWriteOmitsEmptyOptionalFields(t *testing.T) {
	rec := httptest.NewRecorder()
	apierror.Write(rec, apierror.New(apierror.StatusNotFound, "", "no such session"))
	s := rec.Body.String()
	if strings.Contains(s, "reason") || strings.Contains(s, "retry_after_seconds") || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("optional fields must be omitted: %s", s)
	}
	if rec.Code != 404 {
		t.Fatal(rec.Code)
	}
}

func TestWriteInternalHidesErrorAndLogs(t *testing.T) {
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	rec := httptest.NewRecorder()
	apierror.WriteInternal(rec, logger, "req-1", errors.New("pg: connection refused"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "connection refused") || !strings.Contains(rec.Body.String(), `"message":"internal error"`) || !strings.Contains(rec.Body.String(), `"status":"INTERNAL"`) {
		t.Fatalf("500 body: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), "connection refused") || !strings.Contains(logs.String(), "req-1") {
		t.Fatalf("log must carry err and request id: %s", logs.String())
	}
}

func TestWriteOAuth(t *testing.T) {
	rec := httptest.NewRecorder()
	apierror.WriteOAuth(rec, http.StatusForbidden, "invalid_grant", "user is frozen", map[string]any{"reason": "USER_FROZEN"})
	if rec.Code != 403 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["error"] != "invalid_grant" || body["error_description"] != "user is frozen" || body["reason"] != "USER_FROZEN" {
		t.Fatalf("body: %v", body)
	}
	rec = httptest.NewRecorder()
	apierror.WriteOAuth(rec, 400, "invalid_request", "bad body", nil)
	if strings.Contains(rec.Body.String(), "reason") {
		t.Fatal("no extra keys when extra is nil")
	}
}

func TestWriteRouteNotFoundAndMethodNotAllowed(t *testing.T) {
	rec := httptest.NewRecorder()
	apierror.WriteRouteNotFound(rec)
	var body struct {
		Error struct {
			Code   int    `json:"code"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"error"`
	}
	if rec.Code != 404 {
		t.Fatalf("route not found: %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != 404 || body.Error.Status != "NOT_FOUND" || body.Error.Reason != "ROUTE_NOT_FOUND" {
		t.Fatalf("body: %+v", body)
	}

	rec = httptest.NewRecorder()
	apierror.WriteMethodNotAllowed(rec)
	if rec.Code != 405 {
		t.Fatalf("method not allowed: %d", rec.Code)
	}
	body = struct {
		Error struct {
			Code   int    `json:"code"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"error"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != 405 || body.Error.Status != "INVALID_ARGUMENT" || body.Error.Reason != "METHOD_NOT_ALLOWED" {
		t.Fatalf("body: %+v", body)
	}
}
