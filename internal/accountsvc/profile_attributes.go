package accountsvc

import (
	"io"
	"os"

	"github.com/bbxx111/accountkit/user"
)

const profileSchemaMaxBytes = 64 * 1024

// loadProfileAttributes 仅在服务配置阶段读取一次本地 Schema；库配置保持无文件 I/O。
func loadProfileAttributes(path string) (*user.ProfileAttributesConfig, error) {
	if path == "" {
		return nil, nil
	}
	configError := func(category string) error {
		return invalidConfig("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE (" + category + ")")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, configError("unreadable file")
	}
	// 在打开前拒绝 FIFO、设备等特殊文件，避免等待写端或读取无限数据。
	if !info.Mode().IsRegular() {
		return nil, configError("not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, configError("unreadable file")
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		return nil, configError("unreadable file")
	}
	if !info.Mode().IsRegular() {
		return nil, configError("not a regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, profileSchemaMaxBytes+1))
	if err != nil {
		return nil, configError("unreadable file")
	}
	if len(raw) == 0 {
		return nil, configError("empty file")
	}
	if len(raw) > profileSchemaMaxBytes {
		return nil, configError("file exceeds 64 KiB")
	}
	config := &user.ProfileAttributesConfig{Schema: raw}
	if err := user.ValidateProfileAttributesConfig(config); err != nil {
		return nil, configError("invalid Schema")
	}
	return config, nil
}
