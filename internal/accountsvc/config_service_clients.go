package accountsvc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/bbxx111/accountkit/internal/accountsvc/serviceauth"
)

var serviceClientAliasPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,31}$`)

var legacyServiceVariables = []string{
	"ACCOUNTSVC_INTROSPECTION_BASIC_CLIENTS",
	"ACCOUNTSVC_INTROSPECTION_BEARER_CLIENTS",
	"ACCOUNTSVC_PURGE_FEED_BASIC_CLIENTS",
	"ACCOUNTSVC_PURGE_FEED_BEARER_CLIENTS",
}

// serviceClientCSV 仅裁剪元素两端空白，拒绝空项和重复项。
func serviceClientCSV(raw, variable string) ([]string, error) {
	items := strings.Split(raw, ",")
	seen := map[string]bool{}
	for i, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			return nil, invalidConfig(variable)
		}
		seen[item] = true
		items[i] = item
	}
	return items, nil
}

// loadServiceClients 始终构造注册表，并拒绝已移除的逐接口配置。
// 别名可含下划线，因此生成完整预期变量名，不拆分后缀推断别名。
func (c *Config) loadServiceClients() error {
	raw := os.Getenv("ACCOUNTSVC_SERVICE_CLIENTS")
	for _, name := range legacyServiceVariables {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return errors.New("accountsvc: " + name + " has been removed; use ACCOUNTSVC_SERVICE_CLIENTS")
		}
	}
	var aliases []string
	var err error
	if strings.TrimSpace(raw) != "" {
		aliases, err = serviceClientCSV(raw, "ACCOUNTSVC_SERVICE_CLIENTS")
		if err != nil {
			return err
		}
	}
	expected := map[string]bool{}
	for _, alias := range aliases {
		if !serviceClientAliasPattern.MatchString(alias) {
			return invalidConfig("ACCOUNTSVC_SERVICE_CLIENTS")
		}
		for _, field := range []string{"ID", "AUTH_METHODS", "PERMISSIONS", "BASIC_SECRETS", "BEARER_SUBJECTS"} {
			expected["ACCOUNTSVC_SERVICE_CLIENT_"+alias+"_"+field] = true
		}
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "ACCOUNTSVC_SERVICE_CLIENT_") && !expected[name] {
			return invalidConfig(name + " (undeclared client or unknown field)")
		}
	}
	clients := make([]serviceauth.ClientConfig, 0, len(aliases))
	ids := map[string]bool{}
	secrets := map[[sha256.Size]byte]bool{}
	for _, alias := range aliases {
		prefix := "ACCOUNTSVC_SERVICE_CLIENT_" + alias + "_"
		id := os.Getenv(prefix + "ID") // ID 中的空白非法，不得先规范化。
		if !serviceauth.ValidClientID(id) || strings.ContainsRune(id, ':') || ids[id] {
			return invalidConfig(prefix + "ID")
		}
		ids[id] = true
		methods, err := serviceClientCSV(os.Getenv(prefix+"AUTH_METHODS"), prefix+"AUTH_METHODS")
		if err != nil {
			return err
		}
		for _, method := range methods {
			if method != serviceauth.MethodBasic && method != serviceauth.MethodBearer {
				return invalidConfig(prefix + "AUTH_METHODS")
			}
		}
		permissions, err := serviceClientCSV(os.Getenv(prefix+"PERMISSIONS"), prefix+"PERMISSIONS")
		if err != nil {
			return err
		}
		for _, permission := range permissions {
			if !serviceauth.ValidPermission(permission) {
				return invalidConfig(prefix + "PERMISSIONS")
			}
		}
		var basicSecrets []string
		secretText := os.Getenv(prefix + "BASIC_SECRETS")
		if slices.Contains(methods, serviceauth.MethodBasic) {
			basicSecrets, err = serviceClientCSV(secretText, prefix+"BASIC_SECRETS")
			if err != nil {
				return err
			}
			for _, secret := range basicSecrets {
				decoded, err := base64.StdEncoding.Strict().DecodeString(secret)
				digest := sha256.Sum256([]byte(secret))
				if err != nil || len(decoded) < 32 || base64.StdEncoding.EncodeToString(decoded) != secret || secrets[digest] {
					return invalidConfig(prefix + "BASIC_SECRETS")
				}
				secrets[digest] = true
			}
		} else if strings.TrimSpace(secretText) != "" {
			return invalidConfig(prefix + "BASIC_SECRETS")
		}
		var subjects []string
		subjectText := os.Getenv(prefix + "BEARER_SUBJECTS")
		if slices.Contains(methods, serviceauth.MethodBearer) {
			// JSON preserves spaces and commas in provider subjects; values match exactly.
			if json.Unmarshal([]byte(subjectText), &subjects) != nil || len(subjects) == 0 {
				return invalidConfig(prefix + "BEARER_SUBJECTS")
			}
			seen := map[string]bool{}
			for _, subject := range subjects {
				if !serviceauth.ValidBearerSubject(subject) || seen[subject] {
					return invalidConfig(prefix + "BEARER_SUBJECTS")
				}
				seen[subject] = true
			}
		} else if subjectText != "" {
			return invalidConfig(prefix + "BEARER_SUBJECTS")
		}
		if slices.Contains(methods, serviceauth.MethodBearer) {
			c.Service.bearerClientIDs = append(c.Service.bearerClientIDs, id)
		}
		clients = append(clients, serviceauth.ClientConfig{ID: id, AuthMethods: methods, Permissions: permissions, BasicSecrets: basicSecrets, BearerSubjects: subjects})
	}
	c.ServiceClients, err = serviceauth.NewRegistry(clients)
	if err != nil {
		return invalidConfig("ACCOUNTSVC_SERVICE_CLIENTS")
	}
	return nil
}
