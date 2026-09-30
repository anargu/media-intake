package capture

import (
	"uuid"
)

// | Column | Type | Purpose and constraints |
// |---|---|---|
// | `id` | `uuid` | Primary key |
// | `idempotency_key` | `text` | Required; unique |
// | `device_id` | `text` | Required manifest field |
// | `captured_at` | `timestamptz` | Required manifest field |
// | `amount` | `numeric` | Required decimal value; never converted through floating point |
// | `currency` | `text` | Required manifest field |
// | `frame_path` | `text` | Required path to the image on the mounted volume |
// | `frame_size` | `bigint` | Image size, constrained to the supported limit |
// | `created_at` | `timestamptz` | Server creation time |

type Capture struct {
	ID             uuid.UUID `json:"id"`
	IdempotencyKey string    `json:"idempotency_key"`
	DeviceID       string    `json:"device_id"`
	CapturedAt     string    `json:"captured_at"`
	Amount         string    `json:"amount"`
	Currency       string    `json:"currency"`
	FramePath      string    `json:"frame_path"`
	FrameSize      int64     `json:"frame_size"`
	CreatedAt      string    `json:"created_at"`
}
