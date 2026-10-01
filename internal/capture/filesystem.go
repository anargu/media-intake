package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type FileSystemStorage struct {
	rootDir       string
	maxFrameBytes int64 // Max Size
	stagingDir    string
}

const hardMaxFrameBytes = 10 * 1024 * 1024 // 10MB

func NewFileSystemStorage(rootDir string, maxFrameBytes int64) (*FileSystemStorage, error) {
	if rootDir == "" {
		return nil, errors.New("rootDir must not be empty")
	}
	if maxFrameBytes <= 0 {
		return nil, fmt.Errorf("maxFrameBytes must be positive, got %d", maxFrameBytes)
	}
	if maxFrameBytes > hardMaxFrameBytes {
		return nil, fmt.Errorf("maxFrameBytes must not exceed 10MB, got %d", maxFrameBytes)
	}

	stagingDir := filepath.Join(rootDir, ".staging")
	// Creating dirs
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}
	if err := checkDirectoryWritable(rootDir); err != nil {
		return nil, fmt.Errorf("storage root is not writable: %w", err)
	}
	if err := checkDirectoryWritable(stagingDir); err != nil {
		return nil, fmt.Errorf("staging directory is not writable: %w", err)
	}

	return &FileSystemStorage{
		rootDir:       rootDir,
		maxFrameBytes: maxFrameBytes,
		stagingDir:    stagingDir,
	}, nil
}

func checkDirectoryWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return err
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Remove(name); err != nil {
		return err
	}
	return nil
}
