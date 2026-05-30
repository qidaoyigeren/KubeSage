package resilience

import (
	"context"
	"time"
)

type RetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
}

// Do runs a best-effort exponential backoff retry loop. It is intentionally
// small and context-aware so callers can reuse the diagnosis task timeout.
func Do(ctx context.Context, cfg RetryConfig, operation func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := cfg.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	backoff := cfg.InitialBackoff
	if backoff <= 0 {
		backoff = 100 * time.Millisecond
	}
	maxBackoff := cfg.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = backoff
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := operation(); err != nil {
			lastErr = err
		} else {
			return nil
		}
		if attempt == attempts {
			break
		}
		if err := sleep(ctx, backoff); err != nil {
			return err
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return lastErr
}

// FromMilliseconds converts integer config fields into a RetryConfig.
func FromMilliseconds(maxAttempts, initialBackoffMS, maxBackoffMS int) RetryConfig {
	return RetryConfig{
		MaxAttempts:    maxAttempts,
		InitialBackoff: time.Duration(initialBackoffMS) * time.Millisecond,
		MaxBackoff:     time.Duration(maxBackoffMS) * time.Millisecond,
	}
}

// sleep waits for one backoff interval or exits early when the context ends.
func sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
