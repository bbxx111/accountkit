package accountsvc

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
	"github.com/bbxx111/accountkit/user"
)

func TestWarnUnconfiguredInterfaces(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	warnUnconfigured(Config{}, logger)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("want one warning per unconfigured interface, got:\n%s", buf.String())
	}
	for i, want := range []string{"IDENTITY_RESOLUTION_NOT_CONFIGURED", "introspection_not_configured", "PURGE_FEED_NOT_CONFIGURED", "ADMIN_NOT_CONFIGURED", "PROFILE_ATTRIBUTES_NOT_CONFIGURED", "ACCOUNTSVC_SMTP_TEMPLATES_FILE"} {
		if !strings.Contains(lines[i], "level=WARN") || !strings.Contains(lines[i], want) {
			t.Fatalf("line %d = %q, want WARN mentioning %s", i, lines[i], want)
		}
	}

	for name, method := range map[string]string{"basic": "basic", "bearer": "bearer"} {
		registry, err := serviceauth.NewRegistry([]serviceauth.ClientConfig{{ID: "business", AuthMethods: []string{method}, BearerSubjects: map[string][]string{"bearer": {"machine"}}[method], Permissions: []string{"tokens.introspect", "purged_users.read", "identities.resolve"}, BasicSecrets: map[string][]string{"basic": {basicSecretForWarnings()}}[method]}})
		if err != nil {
			t.Fatal(err)
		}
		cfg := Config{ServiceClients: registry, AdminEnabled: true}
		t.Run(name, func(t *testing.T) {
			buf.Reset()
			cfg.Library.ProfileAttributes = &user.ProfileAttributesConfig{}
			cfg.EmailTemplates = emailtemplates.Default()
			warnUnconfigured(cfg, logger)
			if buf.Len() != 0 {
				t.Fatalf("configured interfaces warned:\n%s", buf.String())
			}
		})
	}
}

func TestServiceCapabilityWarnings(t *testing.T) {
	for _, permissions := range [][]string{nil, {"tokens.introspect"}, {"purged_users.read"}, {"tokens.introspect", "purged_users.read"}} {
		t.Run(strings.Join(permissions, ","), func(t *testing.T) {
			var clients []serviceauth.ClientConfig
			if len(permissions) > 0 {
				clients = []serviceauth.ClientConfig{{ID: "private-client-id", AuthMethods: []string{"bearer"}, BearerSubjects: []string{"machine"}, Permissions: permissions}}
			}
			registry, err := serviceauth.NewRegistry(clients)
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{ServiceClients: registry, AdminEnabled: true, EmailTemplates: emailtemplates.Default()}
			cfg.Library.ProfileAttributes = &user.ProfileAttributesConfig{}
			var buf bytes.Buffer
			warnUnconfigured(cfg, slog.New(slog.NewTextHandler(&buf, nil)))
			out := buf.String()
			for _, capability := range []struct{ permission, marker string }{{"tokens.introspect", "introspection is not configured"}, {"purged_users.read", "purged user feed is not configured"}, {"identities.resolve", "identity resolution is not configured"}} {
				want := 1
				if registry.HasPermission(capability.permission) {
					want = 0
				}
				if got := strings.Count(out, capability.marker); got != want {
					t.Fatalf("warning count %d want %d: %s", got, want, out)
				}
			}
			if out != "" && (!strings.Contains(out, "ACCOUNTSVC_SERVICE_CLIENTS") || !strings.Contains(out, "PERMISSIONS")) {
				t.Fatalf("missing unified settings category: %s", out)
			}
			if strings.Contains(out, "private-client-id") || strings.Contains(out, "BASIC_CLIENTS") {
				t.Fatalf("unified warning contains values or legacy settings: %s", out)
			}
		})
	}
	out := runServe(t, 3*time.Second, func(*Config, *application) {}, nil)
	if strings.Contains(out, "deprecated") || strings.Contains(out, "BASIC_CLIENTS") || strings.Contains(out, "BEARER_CLIENTS") {
		t.Fatalf("startup retained removed configuration warning: %s", out)
	}
}

func basicSecretForWarnings() string { return "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" }
