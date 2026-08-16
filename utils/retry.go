package utils

import (
	"context"
	"errors"
	"math/rand"
	"time"
)

// RetryOptions configures retry behavior with exponential backoff
type RetryOptions struct {
	MaxAttempts int           // Maximum number of attempts (0 = unlimited)
	BaseDelay   time.Duration // Initial delay between retries
	MaxDelay    time.Duration // Maximum delay between retries
	Jitter      float64       // Jitter factor (0.0-1.0) for randomization
}

// DefaultRetryOptions returns sensible defaults for retry behavior
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxAttempts: 10,
		BaseDelay:   1 * time.Second,
		MaxDelay:    5 * time.Minute,
		Jitter:      0.2, // ±20% jitter
	}
}

// RetryableError wraps an error to indicate it should be retried
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// NewRetryableError creates a new retryable error
func NewRetryableError(err error) *RetryableError {
	return &RetryableError{Err: err}
}

// IsRetryable checks if an error should be retried
func IsRetryable(err error) bool {
	var retryableErr *RetryableError
	return errors.As(err, &retryableErr)
}

// Retry executes fn with exponential backoff until it succeeds or max attempts reached.
// If fn returns a RetryableError, it will be retried. Otherwise, the error is returned immediately.
// If fn returns nil, Retry returns nil immediately.
func Retry(ctx context.Context, fn func() error, opts RetryOptions) error {
	_, err := RetryWithResult(ctx, func() (struct{}, error) {
		return struct{}{}, fn()
	}, opts)
	return err
}

// RetryWithResult executes fn with exponential backoff and returns the result.
// If fn returns a RetryableError, it will be retried. Otherwise, the error is returned immediately.
func RetryWithResult[T any](ctx context.Context, fn func() (T, error), opts RetryOptions) (T, error) {
	var zero T
	var lastErr error

	for attempt := 1; opts.MaxAttempts == 0 || attempt <= opts.MaxAttempts; attempt++ {
		// Check context before each attempt
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return zero, lastErr
			}
			return zero, ctx.Err()
		default:
		}

		result, err := fn()
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Only retry if it's a retryable error
		if !IsRetryable(err) {
			return zero, err
		}

		// Don't sleep after last attempt
		if opts.MaxAttempts > 0 && attempt >= opts.MaxAttempts {
			break
		}

		// Calculate delay with exponential backoff
		delay := CalculateBackoff(attempt, opts)

		select {
		case <-ctx.Done():
			return zero, lastErr
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return zero, lastErr
}

// CalculateBackoff calculates the delay for a given attempt using exponential backoff with jitter.
// attempt is 1-based (first attempt = 1).
// This is exported so it can be reused by other packages (e.g., EventSub reconnection).
func CalculateBackoff(attempt int, opts RetryOptions) time.Duration {
	// Exponential backoff: baseDelay * 2^(attempt-1)
	delay := opts.BaseDelay * time.Duration(1<<uint(attempt-1))

	// Cap at max delay
	if delay > opts.MaxDelay {
		delay = opts.MaxDelay
	}

	// Apply jitter (±jitter%)
	if opts.Jitter > 0 {
		jitterRange := float64(delay) * opts.Jitter
		jitterOffset := (rand.Float64()*2 - 1) * jitterRange // -jitter to +jitter
		delay = time.Duration(float64(delay) + jitterOffset)
	}

	// Ensure delay is at least baseDelay
	if delay < opts.BaseDelay {
		delay = opts.BaseDelay
	}

	return delay
}

// RetryForever executes fn with exponential backoff indefinitely until success or context cancellation.
// Useful for operations that must eventually succeed (like reconnection).
func RetryForever(ctx context.Context, fn func() error, opts RetryOptions) error {
	opts.MaxAttempts = 0 // Unlimited
	return Retry(ctx, fn, opts)
}
