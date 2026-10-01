package capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type storageFileOps struct {
	link          func(string, string) error
	syncDirectory func(string) error
	remove        func(string) error
}
type FileSystemStorage struct {
	rootDir       string
	maxFrameBytes int64 // Max Size
	stagingDir    string
	fileOps       storageFileOps
}

const hardMaxFrameBytes = 10 * 1024 * 1024 // 10MB

var errPartialStagingCleanup = errors.New("failed to clean up partial staged frame")

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
	if os.MkdirAll(stagingDir, 0o755) != nil {
		return nil, errors.New("failed to create staging directory")
	}
	if checkDirectoryWritable(rootDir) != nil {
		return nil, errors.New("storage root is not writable")
	}
	if checkDirectoryWritable(stagingDir) != nil {
		return nil, errors.New("staging directory is not writable")
	}

	storage := &FileSystemStorage{
		rootDir:       rootDir,
		maxFrameBytes: maxFrameBytes,
		stagingDir:    stagingDir,
		fileOps: storageFileOps{
			link:          os.Link,
			syncDirectory: syncDirectory,
			remove:        os.Remove,
		},
	}

	if err := storage.cleanupStaleStagingFiles(time.Now()); err != nil {
		return nil, errors.New("failed to clean up stale staging files")
	}

	return storage, nil
}

// startup check
func checkDirectoryWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return errors.New("directory write probe failed")
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		_ = os.Remove(name)
		return errors.New("directory write probe failed")
	}
	if err := os.Remove(name); err != nil {
		return errors.New("directory write probe failed")
	}
	return nil
}

func (s *FileSystemStorage) Stage(ctx context.Context, src io.Reader) (staged *StagedFrame, retErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	file, err := os.CreateTemp(s.stagingDir, "frame-*.part")
	if err != nil {
		return nil, errors.New("failed to create staging file")
	}

	filePath := file.Name()
	keepFile := false

	defer func() {
		if !keepFile {
			closeErr := file.Close()
			removeErr := os.Remove(filePath)

			if closeErr != nil ||
				(removeErr != nil && !errors.Is(removeErr, os.ErrNotExist)) {
				retErr = errors.Join(retErr, errPartialStagingCleanup)
			}
		}
	}()

	hasher := sha256.New()
	limitedReader := io.LimitReader(
		contextReader{ctx: ctx, reader: src},
		s.maxFrameBytes+1,
	)

	size, err := io.Copy(io.MultiWriter(file, hasher), limitedReader)
	if err != nil {
		return nil, errors.New("failed to write to staging file")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if size == 0 {
		return nil, errors.New("frame is empty")
	}
	if size > s.maxFrameBytes {
		return nil, errors.New("frame exceeds max bytes to process")
	}
	if err := file.Sync(); err != nil {
		return nil, errors.New("could not sync staging file to disk")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("failed to close staging file")
	}
	keepFile = true

	stagedFrame := &StagedFrame{
		storage:     s,
		stagingPath: filePath,
		size:        size,
		sha256:      hex.EncodeToString(hasher.Sum(nil)),
	}

	return stagedFrame, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (cr contextReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.reader.Read(p)
}

func (s *FileSystemStorage) cleanupStaleStagingFiles(now time.Time) error {
	entries, err := os.ReadDir(s.stagingDir)
	if err != nil {
		return errors.New("failed to read staging directory")
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return errors.New("failed to inspect staging entry")
		}

		if !info.Mode().IsRegular() {
			continue
		}

		// Remove files older than 1 hour
		if now.Sub(info.ModTime()) > 1*time.Hour {
			filename := entry.Name()
			// Skip removing files that don't have the ".part" suffix
			if !strings.HasSuffix(filename, ".part") {
				continue
			}

			if err := os.Remove(filepath.Join(s.stagingDir, entry.Name())); err != nil {
				return errors.New("failed to remove stale staging file")
			}
		}
	}
	return nil
}
