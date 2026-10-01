package capture

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestCaptureResponseMarshalsAmountWithTwoFractionalDigits(t *testing.T) {
	amount, err := decimal.NewFromString("12.00")
	if err != nil {
		t.Fatalf("parse test amount: %v", err)
	}

	data, err := json.Marshal(CaptureResponse{
		ID:         "capture-id",
		CapturedAt: time.Date(2026, time.September, 28, 12, 0, 0, 0, time.FixedZone("offset", 3*60*60)),
		Amount:     amount,
		Currency:   "USD",
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	var got struct {
		Amount string `json:"amount"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Amount != "12.00" {
		t.Errorf("serialized amount = %q, want %q; JSON: %s", got.Amount, "12.00", data)
	}
}
