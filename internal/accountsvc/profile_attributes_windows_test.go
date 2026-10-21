package accountsvc

import (
	"syscall"
	"testing"
)

func exclusiveProfileFile(t *testing.T, path string) syscall.Handle {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal("profile Schema file remains open or cannot be exclusively opened")
	}
	return handle
}

func TestProfileAttributesUnreadableFile(t *testing.T) {
	serveEnvironment(t)
	path := profileSchemaFile(t, serviceProfileSchema)
	handle := exclusiveProfileFile(t, path)
	t.Cleanup(func() { _ = syscall.CloseHandle(handle) })
	t.Setenv("ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE", path)
	_, err := LoadConfig("serve")
	assertProfileSchemaError(t, err, path)
}

func TestProfileAttributesFileClosed(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"valid", serviceProfileSchema},
		{"invalid", "synthetic-private-schema-content"},
		{"empty", ""},
		{"over limit", string(make([]byte, 64*1024+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := profileSchemaFile(t, tc.content)
			_, err := loadProfileAttributes(path)
			if tc.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if tc.name != "valid" {
				assertProfileSchemaError(t, err, path, tc.content)
			}
			// Windows 禁止在已有未共享的读句柄存活时独占打开；此处验证所有返回路径释放文件。
			handle := exclusiveProfileFile(t, path)
			if err := syscall.CloseHandle(handle); err != nil {
				t.Fatal(err)
			}
		})
	}
}
