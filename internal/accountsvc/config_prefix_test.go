package accountsvc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
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
	var sources []*ast.File
	definitions := map[string]ast.Expr{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, source)
		ast.Inspect(source, func(node ast.Node) bool {
			decl, ok := node.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return true
			}
			for _, spec := range decl.Specs {
				value := spec.(*ast.ValueSpec)
				for i, name := range value.Names {
					if i < len(value.Values) {
						definitions[name.Name] = value.Values[i]
					}
				}
			}
			return false
		})
	}
	var stringValue func(ast.Expr, map[string]bool) (string, bool)
	stringValue = func(expr ast.Expr, visiting map[string]bool) (string, bool) {
		switch expr := expr.(type) {
		case *ast.BasicLit:
			if expr.Kind == token.STRING {
				value, err := strconv.Unquote(expr.Value)
				return value, err == nil
			}
		case *ast.Ident:
			if !visiting[expr.Name] {
				visiting[expr.Name] = true
				defer delete(visiting, expr.Name)
				return stringValue(definitions[expr.Name], visiting)
			}
		case *ast.BinaryExpr:
			if expr.Op == token.ADD {
				left, lok := stringValue(expr.X, visiting)
				right, rok := stringValue(expr.Y, visiting)
				return left + right, lok && rok
			}
		}
		return "", false
	}
	for _, source := range sources {
		ast.Inspect(source, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fullName := false
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name != "env" && fun.Name != "defaultEnv" && fun.Name != "durationEnv" {
					return true
				}
			case *ast.SelectorExpr:
				pkg, ok := fun.X.(*ast.Ident)
				if !ok || pkg.Name != "os" || (fun.Sel.Name != "Getenv" && fun.Sel.Name != "LookupEnv") {
					return true
				}
				fullName = true
			default:
				return true
			}
			// Library error-name lookups deliberately use the caller prefix;
			// they are not independent service configuration readers.
			if joined, ok := call.Args[0].(*ast.BinaryExpr); ok {
				if prefix, ok := joined.X.(*ast.Ident); ok && prefix.Name == "envPrefix" {
					return true
				}
			}
			name, ok := stringValue(call.Args[0], map[string]bool{})
			if fullName {
				var found bool
				name, found = strings.CutPrefix(name, "ACCOUNTSVC_")
				ok = ok && found
			}
			if ok && name != "" && !strings.HasSuffix(name, "_") {
				seen[name] = true
			}
			return true
		})
	}
	for _, key := range aliyunTemplateKeys {
		seen["SMS_ALIYUN_"+key.suffix] = true
	}
	// Service client fields are read through a dynamically constructed prefix,
	// so literal-reader scanning cannot discover them. Probe a valid alias with
	// an underscore, exercising the same namespace as deployed client settings.
	for _, field := range []string{"ID", "AUTH_METHODS", "PERMISSIONS", "BASIC_SECRETS"} {
		seen["SERVICE_CLIENT_COLLISION_PROBE_"+field] = true
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
	for _, required := range []string{"DATABASE_URL", "HTTP_ADDR", "SERVICE_CLIENTS", "SERVICE_CLIENT_COLLISION_PROBE_ID", "SERVICE_CLIENT_COLLISION_PROBE_AUTH_METHODS", "SERVICE_CLIENT_COLLISION_PROBE_PERMISSIONS", "SERVICE_CLIENT_COLLISION_PROBE_BASIC_SECRETS", "SMTP_HOST", "SMS_ALIYUN_TEMPLATE_SIGN_IN"} {
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
