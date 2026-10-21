package accountsvc

import (
	"os"
	"strings"
	"testing"
)

func TestProfileAttributesReadFailure(t *testing.T) {
	// 此进程的 mem 是可打开的普通文件，但从偏移 0 读取返回 EIO。
	// 区别于缺失/无权限文件，此用例实际到达有界读取错误分支。
	file, err := os.Open("/proc/self/mem")
	if err != nil {
		t.Fatal("read-error fixture must be openable")
	}
	info, err := file.Stat()
	_ = file.Close()
	if err != nil || !info.Mode().IsRegular() {
		t.Fatal("read-error fixture must be a regular file")
	}
	_, err = loadProfileAttributes("/proc/self/mem")
	assertProfileSchemaError(t, err, "/proc/self/mem")
	if !strings.Contains(err.Error(), "unreadable file") {
		t.Fatal("failed read was not classified as unreadable")
	}
}
