package outbox

import "uuid"

type Outbox struct {
	ID           uuid.UUID `json:"id"`
	CaptureID    uuid.UUID `json:"capture_id"`
	Status       string    `json:"status"`
	AttemptCount int       `json:"attempt_count"`
	LastError    string    `json:"last_error"`
	CreatedAt    string    `json:"created_at"`
	DeliveredAt  string    `json:"delivered_at"`
}
