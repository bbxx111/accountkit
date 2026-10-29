package accountsvc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/adminauth"
)

const (
	plainIssuerWarnAdmin   = "accountsvc: administrator issuer uses plain HTTP; allowed only in development mode"
	plainIssuerWarnService = "accountsvc: service token issuer uses plain HTTP; allowed only in development mode"
)

func TestWarnPlainHTTPIssuers(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	cfg := Config{
		AdminEnabled: true,
		Admin:        adminauth.Config{Issuer: "http://synthetic-secret.example.test/realms/admin", Audience: "admin-api", AllowHTTP: true},
		Service:      ServiceConfig{Issuer: "http://synthetic-secret.example.test/realms/svc", Audience: "svc", IntrospectionBearerClients: []string{"intro"}, AllowHTTP: true},
	}
	warnPlainHTTPIssuers(cfg, logger)
	out := logs.String()
	if !strings.Contains(out, "level=WARN msg=\""+plainIssuerWarnAdmin+"\" settings=ACCOUNTSVC_ADMIN_ISSUER") || !strings.Contains(out, "level=WARN msg=\""+plainIssuerWarnService+"\" settings=ACCOUNTSVC_SERVICE_ISSUER") {
		t.Fatalf("missing plain HTTP warnings: %s", out)
	}
	if strings.Contains(out, "synthetic-secret") || strings.Count(out, "level=WARN") != 2 {
		t.Fatalf("warnings must name only the variables, once each: %s", out)
	}
	logs.Reset()
	// url.Parse lowercases the scheme, so configuration accepts HTTP:// and the WARN must follow.
	cfg.Admin.Issuer, cfg.Service.Issuer = "HTTP://idp.example.test/realms/admin", "Http://idp.example.test/realms/svc"
	warnPlainHTTPIssuers(cfg, logger)
	if strings.Count(logs.String(), "level=WARN") != 2 {
		t.Fatalf("mixed-case scheme must still warn: %s", logs.String())
	}
	logs.Reset()
	cfg.Admin.Issuer, cfg.Service.Issuer = "https://idp.example.test/realms/admin", "https://idp.example.test/realms/svc"
	warnPlainHTTPIssuers(cfg, logger)
	if logs.Len() != 0 {
		t.Fatalf("HTTPS issuers must not warn even in development: %s", logs.String())
	}
	cfg.Admin.Issuer, cfg.Service.Issuer = "http://idp.example.test/realms/admin", "http://idp.example.test/realms/svc"
	cfg.AdminEnabled, cfg.Service.IntrospectionBearerClients = false, nil
	warnPlainHTTPIssuers(cfg, logger)
	if logs.Len() != 0 {
		t.Fatalf("disabled features must not warn: %s", logs.String())
	}
}

// The warnings precede administrator discovery, the first plain-HTTP network access, so the
// log explains any provider failure that follows.
func TestRuntimeWarnsPlainHTTPIssuersBeforeAdminDiscovery(t *testing.T) {
	var logs *safeLog
	var seenAtDiscovery string
	out := runServe(t, 10*time.Second, func(cfg *Config, app *application) {
		cfg.Mode = "development"
		cfg.AdminEnabled = true
		cfg.Admin = adminauth.Config{Issuer: "http://synthetic-secret.example.test/realms/admin", Audience: "admin-api", AllowHTTP: true}
		cfg.Service = ServiceConfig{Issuer: "http://synthetic-secret.example.test/realms/svc", Audience: "svc", PurgeFeedBearerClients: []string{"purge"}, AllowHTTP: true}
		app.initAdmin = func(context.Context) error {
			seenAtDiscovery = logs.String()
			return nil
		}
	}, func(l *safeLog) { logs = l })
	if !strings.Contains(seenAtDiscovery, plainIssuerWarnAdmin) || !strings.Contains(seenAtDiscovery, plainIssuerWarnService) {
		t.Fatalf("warnings must be logged before administrator discovery: %q", seenAtDiscovery)
	}
	if strings.Contains(out, "synthetic-secret") {
		t.Fatalf("issuer value leaked into the log: %s", out)
	}
}
