package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/shopspring/decimal"
)

type Manifest struct {
	CapturedAt time.Time       `json:"capturedAt"`
	Amount     decimal.Decimal `json:"amount"`
	Currency   string          `json:"currency"`
}

func DecodeManifest(r io.Reader) (Manifest, error) {
	var wire struct {
		CapturedAt *string `json:"capturedAt"`
		Amount     *string `json:"amount"`
		Currency   *string `json:"currency"`
	}

	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if wire.CapturedAt == nil || wire.Amount == nil || wire.Currency == nil {
		return Manifest{}, fmt.Errorf("decode manifest: capturedAt, amount, and currency are required")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}

	capturedAt, err := time.Parse(time.RFC3339, *wire.CapturedAt)
	if err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: capturedAt must be RFC3339 with an explicit offset: %w", err)
	}

	amount, err := parseAmount(*wire.Amount)
	if err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if err := ValidateCurrency(*wire.Currency); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}

	return Manifest{
		CapturedAt: capturedAt.UTC(),
		Amount:     amount,
		Currency:   *wire.Currency,
	}, nil
}

// ValidateCurrency validates currency, e.g., "USD", "PEN".
func ValidateCurrency(currency string) error {
	if len(currency) != 3 {
		return errors.New("currency must contain exactly three uppercase ASCII letters")
	}
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'A' || currency[i] > 'Z' {
			return errors.New("currency must contain exactly three uppercase ASCII letters")
		}
	}
	return nil
}

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)

func ValidateIdempotencyKey(key string) error {
	if !idempotencyKeyPattern.MatchString(key) {
		return errors.New("idempotency key must contain 1 to 128 allowed ASCII characters")
	}

	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return fmt.Errorf("invalid trailing data: %w", err)
	}
	return fmt.Errorf("trailing JSON value")
}

// Amount Validation and Parsing
var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})\.[0-9]{2}$`)

func parseAmount(raw string) (decimal.Decimal, error) {
	if !amountPattern.MatchString(raw) {
		return decimal.Zero, errors.New("amount must have exactly two decimal places and fit NUMERIC(20,2)")
	}

	amount, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, fmt.Errorf("parse amount: %w", err)
	}
	return amount, nil
}
