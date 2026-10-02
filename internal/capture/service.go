package capture

import (
	"context"
	"errors"
)

type CaptureService struct{}

func (c *CaptureService) CreateCapture(ctx context.Context, input CreateCaptureInput) (result *CaptureResult, retErr error) {
	defer func() {
		if input.Frame != nil {
			retErr = errors.Join(retErr, input.Frame.Discard())
		}
	}()

	return nil, errors.New("not implemented")
}
