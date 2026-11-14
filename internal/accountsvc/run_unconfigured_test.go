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
	if len(lines) != 5 {
		t.Fatalf("want one warning per unconfigured interface, got:\n%s", buf.String())
	}
	for i, want := range []string{"introspection_not_configured", "PURGE_FEED_NOT_CONFIGURED", "ADMIN_NOT_CONFIGURED", "PROFILE_ATTRIBUTES_NOT_CONFIGURED", "ACCOUNTSVC_SMTP_TEMPLATES_FILE"} {
		if !strings.Contains(lines[i], "level=WARN") || !strings.Contains(lines[i], want) {
			t.Fatalf("line %d = %q, want WARN mentioning %s", i, lines[i], want)
		}
	}

	for name, cfg := range map[string]Config{
		"basic": {
			IntrospectionBasicClients: map[string][]string{"business": {"secret"}},
			PurgeFeedBasicClients:     map[string][]string{"ai-food": {"secret"}},
			AdminEnabled:              true,
		},
		"bearer": {
			Service:      ServiceConfig{IntrospectionBearerClients: []string{"intro"}, PurgeFeedBearerClients: []string{"purge"}},
			AdminEnabled: true,
		},
	} {
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

func TestUnifiedCapabilityAndLegacyWarnings(t *testing.T) {
	for _, permissions := range [][]string{nil, {"tokens.introspect"}, {"purged_users.read"}, {"tokens.introspect", "purged_users.read"}} {
		t.Run(strings.Join(permissions, ","), func(t *testing.T) {
			var clients []serviceauth.ClientConfig
			if len(permissions) > 0 {
				clients = []serviceauth.ClientConfig{{ID: "private-client-id", AuthMethods: []string{"bearer"}, Permissions: permissions}}
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
			for _, capability := range []struct{ permission, marker string }{{"tokens.introspect", "introspection is not configured"}, {"purged_users.read", "purged user feed is not configured"}} {
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
	// This exercises the actual startup path: one grouped deprecation warning per
	// startup, including nonblank empty old JSON placeholders, without values.
	out := runServe(t, 3*time.Second, func(cfg *Config, _ *application) {
		cfg.deprecatedServiceVariables = []string{"ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"}
	}, nil)
	if strings.Count(out, "deprecated") != 1 {
		t.Fatalf("want one legacy deprecation warning at startup: %s", out)
	}
	for _, name := range []string{"ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS", "ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS"} {
		if !strings.Contains(out, name) {
			t.Fatalf("missing deprecated variable %s: %s", name, out)
		}
	}
	if strings.Contains(out, "business") {
		t.Fatalf("legacy warning leaked a client ID: %s", out)
	}
}
