package capture

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"uuid"
)

// Happy path
func TestPublishCreatesHardLinkAndRemovesStagingName(t *testing.T) {
	storage, staged := stageFrameForPublish(t, []byte("frame bytes"))
	id := uuid.UUID{}

	stagingInfo, err := os.Stat(staged.stagingPath)
	if err != nil {
		t.Fatalf("stat staged frame: %v", err)
	}

	result, err := staged.Publish(id)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.State != Published {
		t.Fatalf("Publish() state = %v, want Published", result.State)
	}
	wantRelativePath, _ := finalFrameRelativePath(id)
	if result.RelativePath != wantRelativePath {
		t.Errorf("Publish() relative path = %q, want %q", result.RelativePath, wantRelativePath)
	}
	if result.CleanupWarning != nil {
		t.Errorf("Publish() cleanup warning = %v, want nil", result.CleanupWarning)
	}

	finalPath := filepath.Join(storage.rootDir, wantRelativePath)
	finalInfo, err := os.Stat(finalPath)
	if err != nil {
		t.Fatalf("stat final frame: %v", err)
	}
	if !os.SameFile(stagingInfo, finalInfo) {
		t.Error("published path is not a hard link to the staged inode")
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read published frame: %v", err)
	}
	if !bytes.Equal(got, []byte("frame bytes")) {
		t.Errorf("published bytes = %q, want %q", got, "frame bytes")
	}
	if _, err := os.Lstat(staged.stagingPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging name still exists or could not be checked: %v", err)
	}
}

func TestPublishCollisionDoesNotReplaceExistingFile(t *testing.T) {
	storage, staged := stageFrameForPublish(t, []byte("new frame"))
	id := uuid.UUID{}
	relativePath, err := finalFrameRelativePath(id)
	if err != nil {
		t.Fatalf("build final relative path: %v", err)
	}
	finalPath := filepath.Join(storage.rootDir, relativePath)
	wantExisting := []byte("existing frame")
	if err := os.WriteFile(finalPath, wantExisting, 0o600); err != nil {
		t.Fatalf("create existing final file: %v", err)
	}

	result, err := staged.Publish(id)
	if !errors.Is(err, errFramePublication) {
		t.Fatalf("Publish() error = %v, want errFramePublication", err)
	}
	if result.State != NotPublished {
		t.Errorf("Publish() state = %v, want NotPublished", result.State)
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("read existing final file: %v", err)
	}
	if !bytes.Equal(got, wantExisting) {
		t.Errorf("existing final bytes = %q, want unchanged %q", got, wantExisting)
	}
	if err := staged.Discard(); err != nil {
		t.Errorf("Discard() error = %v", err)
	}
}

func TestPublishDirectorySyncFailureKeepsPublishedState(t *testing.T) {
	storage, staged := stageFrameForPublish(t, []byte("frame"))
	storage.fileOps.syncDirectory = func(string) error { return errors.New("injected sync failure") }

	result, err := staged.Publish(uuid.UUID{})
	if !errors.Is(err, errFinalDirSync) {
		t.Fatalf("Publish() error = %v, want errFinalDirSync", err)
	}
	if result.State != Published {
		t.Errorf("Publish() state = %v, want Published after successful link", result.State)
	}
	if result.RelativePath == "" {
		t.Error("Publish() relative path is empty after successful link")
	}
	if result.CleanupWarning != nil {
		t.Errorf("Publish() cleanup warning = %v, want nil", result.CleanupWarning)
	}
	if _, err := os.Stat(filepath.Join(storage.rootDir, result.RelativePath)); err != nil {
		t.Errorf("final file missing after sync failure: %v", err)
	}
	if _, err := os.Stat(staged.stagingPath); err != nil {
		t.Errorf("staging file missing after sync failure: %v", err)
	}
}

func TestPublishStagingUnlinkFailureIsCleanupWarning(t *testing.T) {
	storage, staged := stageFrameForPublish(t, []byte("frame"))
	storage.fileOps.remove = func(string) error { return errors.New("injected remove failure") }

	result, err := staged.Publish(uuid.UUID{})
	if err != nil {
		t.Fatalf("Publish() error = %v, want nil with cleanup warning", err)
	}
	if result.State != Published {
		t.Errorf("Publish() state = %v, want Published", result.State)
	}
	if !errors.Is(result.CleanupWarning, errStagingUnlink) {
		t.Errorf("Publish() cleanup warning = %v, want errStagingUnlink", result.CleanupWarning)
	}
	if _, err := os.Stat(filepath.Join(storage.rootDir, result.RelativePath)); err != nil {
		t.Errorf("final file missing after unlink failure: %v", err)
	}
	if _, err := os.Stat(staged.stagingPath); err != nil {
		t.Errorf("staging file missing after injected unlink failure: %v", err)
	}
}

func TestDiscardIsIdempotent(t *testing.T) {
	_, staged := stageFrameForPublish(t, []byte("frame"))
	if err := staged.Discard(); err != nil {
		t.Fatalf("first Discard() error = %v", err)
	}
	if err := staged.Discard(); err != nil {
		t.Errorf("second Discard() error = %v, want nil", err)
	}
}

func stageFrameForPublish(t *testing.T, data []byte) (*FileSystemStorage, *StagedFrame) {
	t.Helper()
	storage := newTestFileSystemStorage(t, hardMaxFrameBytes)
	staged, err := storage.Stage(t.Context(), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	return storage, staged
}
