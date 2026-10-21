//go:build unix

package accountsvc

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProfileAttributesNonRegularFile(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "synthetic-private-schema.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, "/dev/null"} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			result := make(chan error, 1)
			go func() {
				_, err := loadProfileAttributes(path)
				result <- err
			}()
			select {
			case err := <-result:
				assertProfileSchemaError(t, err, path)
			case <-time.After(time.Second):
				t.Fatal("loading a non-regular Schema file blocked")
			}
		})
	}
}

func TestProfileAttributesUnreadableFile(t *testing.T) {
	path := profileSchemaFile(t, serviceProfileSchema)
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if file, err := os.Open(path); err == nil {
		_ = file.Close()
		t.Skip("current user can read permission-denied files")
	}
	_, err := loadProfileAttributes(path)
	assertProfileSchemaError(t, err, path)
}
