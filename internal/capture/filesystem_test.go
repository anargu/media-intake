package capture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewFileSystemStorageChecksDirectoriesAreWritable(t *testing.T) {
	t.Run("writable root and staging directory", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "frames")

		storage, err := NewFileSystemStorage(root, hardMaxFrameBytes)
		if err != nil {
			t.Fatalf("NewFileSystemStorage() error = %v", err)
		}
		if storage.rootDir != root {
			t.Errorf("rootDir = %q, want %q", storage.rootDir, root)
		}

		rootEntries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("read storage root: %v", err)
		}
		if len(rootEntries) != 1 || rootEntries[0].Name() != ".staging" {
			t.Errorf("storage root entries = %v, want only .staging (no write-probe artifacts)", rootEntries)
		}

		stagingEntries, err := os.ReadDir(filepath.Join(root, ".staging"))
		if err != nil {
			t.Fatalf("read staging directory: %v", err)
		}
		if len(stagingEntries) != 0 {
			t.Errorf("staging directory contains write-probe artifacts: %v", stagingEntries)
		}
	})

	t.Run("unwritable staging directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root can write to directories without write permission")
		}

		root := filepath.Join(t.TempDir(), "frames")
		staging := filepath.Join(root, ".staging")
		if err := os.MkdirAll(staging, 0o755); err != nil {
			t.Fatalf("create staging directory: %v", err)
		}
		if err := os.Chmod(staging, 0o555); err != nil {
			t.Fatalf("make staging directory read-only: %v", err)
		}
		t.Cleanup(func() {
			_ = os.Chmod(staging, 0o755)
		})

		if _, err := NewFileSystemStorage(root, hardMaxFrameBytes); err == nil {
			t.Fatal("NewFileSystemStorage() error = nil, want unwritable staging directory error")
		}
	})
}
