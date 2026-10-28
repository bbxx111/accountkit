package accountsvc

import (
	"io"
	"os"
	"strconv"
)

// readConfigFile 有界读取一次本地普通文件配置。错误只含变量名与安全分类，不含路径或内容。
func readConfigFile(path, setting string, maxBytes int) ([]byte, error) {
	configError := func(category string) error {
		return invalidConfig(setting + " (" + category + ")")
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
	raw, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return nil, configError("unreadable file")
	}
	if len(raw) == 0 {
		return nil, configError("empty file")
	}
	if len(raw) > maxBytes {
		return nil, configError("file exceeds " + strconv.Itoa(maxBytes/1024) + " KiB")
	}
	return raw, nil
}
