package capture

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

type CaptureResponse struct {
	ID         string          `json:"id"`
	CapturedAt time.Time       `json:"capturedAt"`
	Amount     decimal.Decimal `json:"-"`
	Currency   string          `json:"currency"`
}

func (c CaptureResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		ID         string    `json:"id"`
		CapturedAt time.Time `json:"capturedAt"`
		Amount     string    `json:"amount"`
		Currency   string    `json:"currency"`
	}{
		ID:         c.ID,
		CapturedAt: c.CapturedAt.UTC(),
		Amount:     c.Amount.StringFixed(2),
		Currency:   c.Currency,
	})
}

type CaptureResult struct {
	Duplicate bool            `json:"duplicate"`
	Capture   CaptureResponse `json:"capture"`
}
