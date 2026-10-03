package outbox

import (
	"encoding/json"
	"time"

	"uuid"
)

const EventTypeCaptureAccepted = "capture.accepted"

type Outbox struct {
	ID           uuid.UUID       `json:"id"`
	CaptureID    uuid.UUID       `json:"capture_id"`
	Payload      json.RawMessage `json:"payload"`
	Status       string          `json:"status"`
	AttemptCount int             `json:"attempt_count"`
	LastError    string          `json:"last_error"`
	CreatedAt    string          `json:"created_at"`
	DeliveredAt  string          `json:"delivered_at"`
}

type CaptureAcceptedPayload struct {
	Version int             `json:"version"`
	Capture CaptureMetadata `json:"capture"`
}

type CaptureMetadata struct {
	ID         string    `json:"id"`
	CapturedAt time.Time `json:"capturedAt"`
	Amount     string    `json:"amount"`
	Currency   string    `json:"currency"`
}
