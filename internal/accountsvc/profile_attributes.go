package accountsvc

import (
	"github.com/bbxx111/accountkit/user"
)

const profileSchemaMaxBytes = 64 * 1024

// loadProfileAttributes 仅在服务配置阶段读取一次本地 Schema；库配置保持无文件 I/O。
func loadProfileAttributes(path string) (*user.ProfileAttributesConfig, error) {
	if path == "" {
		return nil, nil
	}
	const setting = "ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE"
	raw, err := readConfigFile(path, setting, profileSchemaMaxBytes)
	if err != nil {
		return nil, err
	}
	config := &user.ProfileAttributesConfig{Schema: raw}
	if err := user.ValidateProfileAttributesConfig(config); err != nil {
		return nil, invalidConfig(setting + " (invalid Schema)")
	}
	return config, nil
}
