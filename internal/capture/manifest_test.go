package capture

import (
	"strings"
	"testing"
	"time"
)

func TestDecodeManifest(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantUTC string
		wantErr bool
	}{
		{
			name:    "offset normalized to UTC",
			input:   `{"capturedAt":"2026-09-28T15:00:00+03:00","amount":"12.34","currency":"USD"}`,
			wantUTC: "2026-09-28T12:00:00Z",
		},
		{name: "missing field", input: `{"capturedAt":"2026-09-28T12:00:00Z","amount":"12.34"}`, wantErr: true},
		{name: "unknown field", input: `{"capturedAt":"2026-09-28T12:00:00Z","amount":"12.34","currency":"USD","extra":1}`, wantErr: true},
		{name: "wrong amount type", input: `{"capturedAt":"2026-09-28T12:00:00Z","amount":12.34,"currency":"USD"}`, wantErr: true},
		{name: "invalid currency", input: `{"capturedAt":"2026-09-28T12:00:00Z","amount":"12.34","currency":"Usd"}`, wantErr: true},
		{name: "timestamp without offset", input: `{"capturedAt":"2026-09-28T12:00:00","amount":"12.34","currency":"USD"}`, wantErr: true},
		{name: "trailing JSON value", input: `{"capturedAt":"2026-09-28T12:00:00Z","amount":"12.34","currency":"USD"} {}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeManifest(strings.NewReader(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatal("DecodeManifest() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeManifest() error = %v", err)
			}
			if got.CapturedAt.Location() != time.UTC {
				t.Errorf("CapturedAt location = %v, want UTC", got.CapturedAt.Location())
			}
			if formatted := got.CapturedAt.Format(time.RFC3339); formatted != tc.wantUTC {
				t.Errorf("CapturedAt = %q, want %q", formatted, tc.wantUTC)
			}
			if got.Amount.StringFixed(2) != "12.34" || got.Currency != "USD" {
				t.Errorf("decoded amount/currency = %q/%q, want 12.34/USD", got.Amount.String(), got.Currency)
			}
		})
	}
}

func TestValidateCurrency(t *testing.T) {
	for _, tc := range []struct {
		value   string
		wantErr bool
	}{
		{value: "USD"},
		{value: "EUR"},
		{value: "US", wantErr: true},
		{value: "USDD", wantErr: true},
		{value: "Usd", wantErr: true},
		{value: "U$D", wantErr: true},
		{value: "ÜSD", wantErr: true},
	} {
		err := ValidateCurrency(tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateCurrency(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
		}
	}
}

func TestValidateIdempotencyKey(t *testing.T) {
	longValid := strings.Repeat("a", 128)
	tooLong := strings.Repeat("a", 129)
	for _, tc := range []struct {
		value   string
		wantErr bool
	}{
		{value: "a"},
		{value: "Az09._~-"},
		{value: longValid},
		{value: "", wantErr: true},
		{value: tooLong, wantErr: true},
		{value: "bad key", wantErr: true},
		{value: "café", wantErr: true},
	} {
		err := ValidateIdempotencyKey(tc.value)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateIdempotencyKey(%q) error = %v, wantErr %v", tc.value, err, tc.wantErr)
		}
	}
}

func TestParseAmount(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{input: "0.00", valid: true},
		{input: "999999999999999999.99", valid: true},
		{input: "9007199254740993.01", valid: true},
		{input: "00.01"},
		{input: "1.2"},
		{input: "1e2"},
		{input: "1000000000000000000.00"},
	} {
		amount, err := parseAmount(tc.input)
		if !tc.valid {
			if err == nil {
				t.Errorf("parseAmount(%q) error = nil, want error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAmount(%q) error = %v", tc.input, err)
			continue
		}
		if got := amount.StringFixed(2); got != tc.input {
			t.Errorf("parseAmount(%q) round-trip = %q", tc.input, got)
		}
	}
}
