package capture

import (
	"time"
	"uuid"
)

type Capture struct {
	ID             uuid.UUID `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	CapturedAt     time.Time `json:"captured_at"`
	Amount         string    `json:"amount"`
	Currency       string    `json:"currency"`
	FramePath      string    `json:"frame_path"`
	FrameSize      int64     `json:"frame_size"`
	CreatedAt      time.Time `json:"created_at"`
}

type CreateCaptureInput struct {
	IdempotencyKey string
	Manifest       Manifest
	Frame          *StagedFrame
}
