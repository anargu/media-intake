package capture

import (
	"errors"
	"strings"
	"testing"

	"uuid"
)

func TestFinalFrameRelativePathUsesLowercaseUUIDHex(t *testing.T) {
	var id uuid.UUID
	path, err := finalFrameRelativePath(id)
	if err != nil {
		t.Fatalf("finalFrameRelativePath() error = %v", err)
	}
	const want = "00000000000000000000000000000000.frame"
	if path != want {
		t.Errorf("finalFrameRelativePath() = %q, want %q", path, want)
	}
}

func TestValidateFinalFramePath(t *testing.T) {
	validPath := strings.Repeat("a", 32) + ".frame"
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{name: "valid path", path: validPath},
		{name: "traversal", path: "../" + validPath, wantErr: true},
		{name: "uppercase hex", path: strings.Repeat("A", 32) + ".frame", wantErr: true},
		{name: "invalid hex", path: strings.Repeat("g", 32) + ".frame", wantErr: true},
		{name: "too short", path: strings.Repeat("a", 31) + ".frame", wantErr: true},
		{name: "wrong extension", path: strings.Repeat("a", 32) + ".jpg", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFinalFramePath(tc.path)
			if tc.wantErr {
				if !errors.Is(err, errInvalidFinalFramePath) {
					t.Errorf("validateFinalFramePath() error = %v, want %v", err, errInvalidFinalFramePath)
				}
				return
			}
			if err != nil {
				t.Errorf("validateFinalFramePath() error = %v, want nil", err)
			}
		})
	}
}
