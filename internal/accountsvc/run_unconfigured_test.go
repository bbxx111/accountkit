package accountsvc

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit/internal/accountsvc/emailtemplates"
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
