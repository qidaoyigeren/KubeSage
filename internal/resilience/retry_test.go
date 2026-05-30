package resilience

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDoRetriesUntilSuccess verifies retry attempts are made before returning.
func TestDoRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), RetryConfig{MaxAttempts: 3, InitialBackoff: time.Nanosecond, MaxBackoff: time.Nanosecond}, func() error {
		attempts++
		if attempts < 2 {
			return errors.New("temporary")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}
