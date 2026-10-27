package accountsvc

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/bbxx111/accountkit"
)

func TestConfigRejectsLegacyPrefix(t *testing.T) {
	for _, command := range []string{"serve", "migrate"} {
		t.Run(command, func(t *testing.T) {
			serveEnvironment(t)
			t.Setenv("ACCOUNTKIT_PURGED_USER_RETENTION", "synthetic-legacy-value")
			_, err := LoadConfig(command)
			if err == nil {
				t.Fatal("legacy ACCOUNTKIT_ variable was silently ignored")
			}
			msg := err.Error()
			if !strings.Contains(msg, "ACCOUNTKIT_PURGED_USER_RETENTION") || !strings.Contains(msg, "ACCOUNTSVC_PURGED_USER_RETENTION") || strings.Contains(msg, "synthetic-legacy-value") {
				t.Fatalf("legacy rejection must name old and new variables without the value: %s", msg)
			}
		})
	}
	t.Run("empty legacy variable is ignored", func(t *testing.T) {
		serveEnvironment(t)
		t.Setenv("ACCOUNTKIT_PURGED_USER_RETENTION", "")
		if _, err := LoadConfig("serve"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestConfigReadsLibrarySettingsWithServicePrefix(t *testing.T) {
	serveEnvironment(t)
	t.Setenv("ACCOUNTSVC_AUTH_SCHEMA", "svc_prefix")
	c, err := LoadConfig("serve")
	if err != nil {
		t.Fatal(err)
	}
	if c.Library.Schema != "svc_prefix" || c.Library.JWTIssuer != "test-issuer" {
		t.Fatal("library settings were not read from ACCOUNTSVC_ variables")
	}
	t.Setenv("ACCOUNTSVC_ACCESS_TOKEN_TTL", "synthetic-bad-duration")
	_, err = LoadConfig("serve")
	if err == nil || !strings.Contains(err.Error(), "ACCOUNTSVC_ACCESS_TOKEN_TTL") || strings.Contains(err.Error(), "synthetic-bad-duration") {
		t.Fatalf("library error must name the ACCOUNTSVC_ variable without its value: %v", err)
	}
}

var serviceEnvReference = regexp.MustCompile(`(?:\benv|defaultEnv|durationEnv)\("([A-Z0-9_]+)"|os\.(?:Getenv|LookupEnv)\("ACCOUNTSVC_([A-Z0-9_]+)"`)

// serviceEnvNames collects the service-only variable suffixes read by accountsvc source files.
// Library names appear in config.go only in error messages or via envPrefix concatenation,
// never through these readers.
func serviceEnvNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range serviceEnvReference.FindAllStringSubmatch(string(data), -1) {
			name := m[1] + m[2]
			// Prefix literals used for scanning (e.g. "ACCOUNTSVC_SMS_ALIYUN_") end with "_".
			if name != "" && !strings.HasSuffix(name, "_") {
				seen[name] = true
			}
		}
	}
	for _, key := range aliyunTemplateKeys {
		seen["SMS_ALIYUN_"+key.suffix] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func libraryConfigWith(t *testing.T, name, value string) (accountkit.Config, error) {
	t.Helper()
	if name != "" {
		t.Setenv("ACCOUNTSVC_"+name, value)
	}
	return accountkit.ConfigFromEnv(envPrefix)
}

func TestServiceAndLibraryEnvNamesDoNotOverlap(t *testing.T) {
	names := serviceEnvNames(t)
	for _, required := range []string{"DATABASE_URL", "HTTP_ADDR", "INTROSPECTION_CLIENTS", "SMTP_HOST", "SMS_ALIYUN_TEMPLATE_SIGN_IN"} {
		if i := sort.SearchStrings(names, required); i == len(names) || names[i] != required {
			t.Fatalf("service variable scan missed %s; got %v", required, names)
		}
	}
	configEnvironment(t)
	baseline, baseErr := libraryConfigWith(t, "", "")
	if baseErr != nil {
		t.Fatal(baseErr)
	}
	const probe = "synthetic-collision-probe"
	for _, name := range names {
		if strings.HasPrefix(name, "ALIPAY_") || strings.HasPrefix(name, "OIDC_") {
			t.Errorf("service variable %s uses a library-generated namespace", name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			got, err := libraryConfigWith(t, name, probe)
			if err != nil || !reflect.DeepEqual(got, baseline) {
				t.Fatalf("library also reads ACCOUNTSVC_%s", name)
			}
		})
	}
	// Positive control: a library-owned name must change the library result.
	if got, err := libraryConfigWith(t, "AUTH_SCHEMA", "probe_schema"); err != nil || reflect.DeepEqual(got, baseline) {
		t.Fatal("collision probe cannot detect a library variable")
	}
}
