package outbox

import (
	"math"
	"testing"
	"time"
)

func TestBackoffScheduleWithInjectedClockAndRandomness(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	for _, test := range []struct {
		attempt  int
		envelope time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 4 * time.Second},
		{math.MaxInt, 4 * time.Second},
	} {
		for _, high := range []bool{false, true} {
			random := func(n int64) int64 {
				if high {
					return n - 1
				}
				return 0
			}
			delay, err := RetryDelay(test.attempt, time.Second, 4*time.Second, random)
			if err != nil {
				t.Fatal(err)
			}
			want := test.envelope / 2
			if high {
				want = test.envelope - time.Nanosecond
			}
			nextAt := clock().Add(delay)
			if !nextAt.Equal(now.Add(want)) {
				t.Fatalf("attempt=%d delay=%s want=%s", test.attempt, delay, want)
			}
		}
	}
}

func TestBackoffIntegerEdges(t *testing.T) {
	for _, test := range []struct {
		attempt             int
		base, ceiling, want time.Duration
	}{
		{1, 1, 1, 1},
		{1, 5, 5, 3},
		{math.MaxInt, 1, time.Duration(math.MaxInt64), time.Duration(math.MaxInt64/2 + 1)},
	} {
		got, err := RetryDelay(test.attempt, test.base, test.ceiling, func(int64) int64 { return 0 })
		if err != nil || got != test.want {
			t.Fatalf("delay=%s err=%v want=%s", got, err, test.want)
		}
	}
}

func TestBackoffRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		attempt       int
		base, ceiling time.Duration
	}{
		{0, 1, 2}, {1, 0, 2}, {1, 2, 1},
	} {
		if _, err := RetryDelay(test.attempt, test.base, test.ceiling, func(int64) int64 { return 0 }); err == nil {
			t.Fatal("invalid parameters accepted")
		}
	}
	if _, err := RetryDelay(1, 2, 4, nil); err == nil {
		t.Fatal("nil randomness accepted")
	}
	if _, err := RetryDelay(1, 2, 4, func(n int64) int64 { return n }); err == nil {
		t.Fatal("out-of-range randomness accepted")
	}
}

func TestRetryAttemptLimitAndPermanentRejection(t *testing.T) {
	for _, test := range []struct {
		kind         DeliveryKind
		attempt, max int
		want         bool
	}{
		{TransientFailure, 1, 3, true}, {TransientFailure, 2, 3, true}, {TransientFailure, 3, 3, false}, {TransientFailure, 4, 3, false}, {TransientFailure, 1, 1, false}, {TransientFailure, 0, 3, false}, {PermanentFailure, 1, 3, false}, {Delivered, 1, 3, false},
	} {
		if got := (DeliveryResult{Kind: test.kind}).ShouldRetry(test.attempt, test.max); got != test.want {
			t.Fatalf("%#v retry=%t", test, got)
		}
	}
}
