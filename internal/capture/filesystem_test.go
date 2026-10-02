package capture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
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

// Happy path
func TestStageWritesFrameAndComputesMetadata(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "one byte", data: []byte{0x2a}},
		{name: "exactly the hard limit", data: bytes.Repeat([]byte{0x5a}, hardMaxFrameBytes)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
			staged, err := storage.Stage(context.Background(), bytes.NewReader(tc.data))
			if err != nil {
				t.Fatalf("Stage() error = %v", err)
			}

			got, err := os.ReadFile(staged.stagingPath)
			if err != nil {
				t.Fatalf("read staged frame: %v", err)
			}
			if !bytes.Equal(got, tc.data) {
				t.Fatal("staged bytes do not match input")
			}
			if staged.size != int64(len(tc.data)) {
				t.Errorf("size = %d, want %d", staged.size, len(tc.data))
			}

			wantHash := sha256.Sum256(tc.data)
			if staged.sha256 != hex.EncodeToString(wantHash[:]) {
				t.Errorf("sha256 = %q, want %q", staged.sha256, hex.EncodeToString(wantHash[:]))
			}
			if filepath.Ext(staged.stagingPath) != ".part" {
				t.Errorf("staging file extension = %q, want .part", filepath.Ext(staged.stagingPath))
			}
		})
	}
}

func TestStageRejectsEmptyAndOversizedFramesAndCleansUp(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{name: "empty"},
		{name: "one byte over hard limit", data: bytes.Repeat([]byte{0x7f}, hardMaxFrameBytes+1)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
			staged, err := storage.Stage(context.Background(), bytes.NewReader(tc.data))
			if err == nil {
				t.Fatalf("Stage() = (%v, nil), want an error", staged)
			}
			if staged != nil {
				t.Errorf("Stage() staged frame = %#v, want nil on error", staged)
			}
			assertStagingDirectoryEmpty(t, storage.stagingDir)
		})
	}
}

func TestStageReadFailureCleansUpPartialFile(t *testing.T) {
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	readErr := errors.New("simulated interrupted read")
	reader := &failAfterBytesReader{data: []byte("partial frame"), err: readErr}

	staged, err := storage.Stage(context.Background(), reader)
	if err == nil {
		t.Fatal("Stage() error = nil, want read failure")
	}
	if staged != nil {
		t.Errorf("Stage() staged frame = %#v, want nil on error", staged)
	}
	assertStagingDirectoryEmpty(t, storage.stagingDir)
}

func TestStageWriteFailureCleansUpPartialFile(t *testing.T) {
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	writeErr := errors.New("simulated staging write failure")
	storage.fileOps.copy = func(dst io.Writer, _ io.Reader) (int64, error) {
		if _, err := dst.Write([]byte("partial frame")); err != nil {
			return 0, err
		}
		return int64(len("partial frame")), writeErr
	}

	staged, err := storage.Stage(context.Background(), bytes.NewReader([]byte("frame")))
	if err == nil {
		t.Fatal("Stage() error = nil, want simulated write failure")
	}
	if staged != nil {
		t.Errorf("Stage() staged frame = %#v, want nil", staged)
	}
	assertStagingDirectoryEmpty(t, storage.stagingDir)
}

func TestStageCanceledContextDoesNotCreateStagingFile(t *testing.T) {
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	staged, err := storage.Stage(ctx, bytes.NewReader([]byte("frame")))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Stage() error = %v, want context.Canceled", err)
	}
	if staged != nil {
		t.Errorf("Stage() staged frame = %#v, want nil on error", staged)
	}
	assertStagingDirectoryEmpty(t, storage.stagingDir)
}

// Happy path
func TestCleanupStaleStagingFilesRemovesOnlyOldPartFiles(t *testing.T) {
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	now := time.Now()
	oldTime := now.Add(-2 * time.Hour)
	recentTime := now.Add(-30 * time.Minute)

	oldPart := filepath.Join(storage.stagingDir, "old.part")
	oldOther := filepath.Join(storage.stagingDir, "old.txt")
	recentPart := filepath.Join(storage.stagingDir, "recent.part")
	partDirectory := filepath.Join(storage.stagingDir, "directory.part")

	for _, path := range []string{oldPart, oldOther, recentPart} {
		if err := os.WriteFile(path, []byte("frame"), 0o600); err != nil {
			t.Fatalf("create test file: %v", err)
		}
	}
	if err := os.Mkdir(partDirectory, 0o700); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	setModTime(t, oldPart, oldTime)
	setModTime(t, oldOther, oldTime)
	setModTime(t, recentPart, recentTime)
	setModTime(t, partDirectory, oldTime)

	if err := storage.cleanupStaleStagingFiles(now); err != nil {
		t.Fatalf("cleanupStaleStagingFiles() error = %v", err)
	}

	assertPathAbsent(t, oldPart)
	assertPathPresent(t, oldOther)
	assertPathPresent(t, recentPart)
	assertPathPresent(t, partDirectory)
}

func TestCleanupStaleStagingFilesKeepsFileAtOneHourCutoff(t *testing.T) {
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	path := filepath.Join(storage.stagingDir, "cutoff.part")
	if err := os.WriteFile(path, []byte("frame"), 0o600); err != nil {
		t.Fatalf("create test file: %v", err)
	}

	setModTime(t, path, time.Now().Add(-2*time.Hour))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat test file: %v", err)
	}
	cleanupNow := info.ModTime().Add(time.Hour)

	if err := storage.cleanupStaleStagingFiles(cleanupNow); err != nil {
		t.Fatalf("cleanupStaleStagingFiles() error = %v", err)
	}
	assertPathPresent(t, path)
}

func newTestFileSystemStorage(t *testing.T, maxFrameBytes int64) *FileSystemStorage {
	t.Helper()
	root := filepath.Join(t.TempDir(), "frames")
	storage, err := NewFileSystemStorage(root, maxFrameBytes)
	if err != nil {
		t.Fatalf("NewFileSystemStorage() error = %v", err)
	}
	return storage
}

func assertStagingDirectoryEmpty(t *testing.T, stagingDir string) {
	t.Helper()
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		t.Fatalf("read staging directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("staging directory contains leftover files: %v", entries)
	}
}

func setModTime(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("set modification time: %v", err)
	}
}

func assertPathAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("path %q still exists or could not be checked: %v", path, err)
	}
}

func assertPathPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("path %q is missing or could not be checked: %v", path, err)
	}
}

type failAfterBytesReader struct {
	data []byte
	err  error
	done bool
}

func (r *failAfterBytesReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}

var _ io.Reader = (*failAfterBytesReader)(nil)
