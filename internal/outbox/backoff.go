package outbox

import (
	"fmt"
	"time"
)

// RetryDelay uses one-based completed attempts and injected randomness.
// Its exponential envelope saturates at ceiling without overflowing.
// Equal jitter is [ceil(envelope/2), envelope) in integer nanoseconds;
// an envelope of one nanosecond yields exactly one nanosecond.
func RetryDelay(attempt int,
	base,
	ceiling time.Duration,
	randomN func(int64) int64) (time.Duration, error) {
	if attempt < 1 || base <= 0 || ceiling < base || randomN == nil {
		return 0, fmt.Errorf("invalid backoff parameters")
	}

	envelope := base
	for i := 1; i < attempt && envelope < ceiling; i++ {
		if envelope > ceiling/2 {
			envelope = ceiling
		} else {
			envelope *= 2
		}
	}

	lower := envelope/2 + envelope%2
	width := int64(envelope - lower)
	if width == 0 {
		return lower, nil
	}

	offset := randomN(width)
	if offset < 0 || offset >= width {
		return 0, fmt.Errorf("random source out of range")
	}
	return lower + time.Duration(offset), nil
}
