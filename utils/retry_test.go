package utils

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetryCancellationPreservesContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := Retry(ctx, func() error {
		cancel()
		return NewRetryableError(errors.New("network unavailable"))
	}, RetryOptions{MaxAttempts: 10, BaseDelay: time.Hour, MaxDelay: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Retry() = %v, want context cancellation", err)
	}
}

func TestBackoffStaysBoundedDuringLongOutages(t *testing.T) {
	opts := RetryOptions{BaseDelay: time.Second, MaxDelay: 2 * time.Minute, Jitter: 0.2}
	for _, attempt := range []int{1, 8, 32, 64, 128, 1000} {
		for range 100 {
			delay := CalculateBackoff(attempt, opts)
			if delay < opts.BaseDelay || delay > opts.MaxDelay {
				t.Fatalf("attempt %d produced unbounded delay %v", attempt, delay)
			}
			if attempt >= 8 && delay < time.Duration(float64(opts.MaxDelay)*0.8) {
				t.Fatalf("attempt %d reset backoff to %v", attempt, delay)
			}
		}
	}
}
