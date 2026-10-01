package capture

import (
	"uuid"
)

type Capture struct {
	ID             uuid.UUID `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	CapturedAt     string    `json:"captured_at"`
	Amount         string    `json:"amount"`
	Currency       string    `json:"currency"`
	FramePath      string    `json:"frame_path"`
	FrameSize      int64     `json:"frame_size"`
	CreatedAt      string    `json:"created_at"`
}
