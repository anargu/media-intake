package capture

import (
	"errors"
	"regexp"
	"strings"

	"uuid"
)

var finalFrameRelativePathPattern = regexp.MustCompile(`^[0-9a-f]{32}\.frame$`)

var errInvalidFinalFramePath = errors.New("invalid final frame path")

func finalFrameRelativePath(id uuid.UUID) (string, error) {
	uuidHex := strings.ReplaceAll(strings.ToLower(id.String()), "-", "")
	relativePath := uuidHex + ".frame"
	if err := validateFinalFramePath(relativePath); err != nil {
		return "", err
	}
	return relativePath, nil
}

func validateFinalFramePath(path string) error {
	if !finalFrameRelativePathPattern.MatchString(path) {
		return errInvalidFinalFramePath
	}
	return nil
}
