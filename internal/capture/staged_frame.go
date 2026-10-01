package capture

import (
	"errors"
	"os"
	"path/filepath"
	"uuid"
)

type PublicationState uint8

const (
	NotPublished PublicationState = iota
	Published
)

type PublicationResult struct {
	State          PublicationState
	RelativePath   string
	CleanupWarning error
}
type StagedFrame struct {
	storage     *FileSystemStorage
	stagingPath string
	size        int64
	sha256      string
}

var (
	errFramePublication = errors.New("atomic frame publication failed")
	errFinalDirSync     = errors.New("failed to sync final directory after frame publication")
	errStagingDiscard   = errors.New("failed to discard staged frame")
	errStagingUnlink    = errors.New("failed to remove staging name after publication")
)

func (sf *StagedFrame) Discard() error {
	if err := sf.storage.fileOps.remove(sf.stagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errStagingDiscard
	}
	return nil
}

func (sf *StagedFrame) Publish(id uuid.UUID) (PublicationResult, error) {
	result := PublicationResult{State: NotPublished}

	relativePath, err := finalFrameRelativePath(id)
	if err != nil {
		return result, err
	}

	finalPath := filepath.Join(sf.storage.rootDir, relativePath)

	if err := sf.storage.fileOps.link(sf.stagingPath, finalPath); err != nil {
		return result, errFramePublication
	}

	result.State = Published
	result.RelativePath = relativePath

	if err := sf.storage.fileOps.syncDirectory(sf.storage.rootDir); err != nil {
		return result, errFinalDirSync
	}

	if err := sf.storage.fileOps.remove(sf.stagingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		result.CleanupWarning = errStagingUnlink
	}

	return result, nil
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
