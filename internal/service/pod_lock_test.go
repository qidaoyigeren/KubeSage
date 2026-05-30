package service

import (
	"context"
	"testing"
	"time"
)

// TestPodLockPreventsDuplicateUntilRelease verifies the in-process per-pod
// diagnosis lock blocks duplicate work and can be released.
func TestPodLockPreventsDuplicateUntilRelease(t *testing.T) {
	lock := &localPodLock{entries: map[string]time.Time{}}
	token, ok, err := lock.TryAcquire(context.Background(), "default/api", time.Minute)
	if err != nil || !ok {
		t.Fatal("first acquire should succeed")
	}
	if _, ok, _ := lock.TryAcquire(context.Background(), "default/api", time.Minute); ok {
		t.Fatal("second acquire should be blocked")
	}
	if err := lock.Release(context.Background(), "default/api", token); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := lock.TryAcquire(context.Background(), "default/api", time.Minute); !ok {
		t.Fatal("acquire after release should succeed")
	}
}

// TestPodLockAllowsExpiredEntry verifies stale locks do not block forever.
func TestPodLockAllowsExpiredEntry(t *testing.T) {
	lock := &localPodLock{entries: map[string]time.Time{}}
	if _, ok, _ := lock.TryAcquire(context.Background(), "default/api", time.Nanosecond); !ok {
		t.Fatal("first acquire should succeed")
	}
	time.Sleep(time.Millisecond)
	if _, ok, _ := lock.TryAcquire(context.Background(), "default/api", time.Minute); !ok {
		t.Fatal("expired acquire should succeed")
	}
}

// TestRedisPodLockKey verifies Redis lock keys are scoped by prefix.
func TestRedisPodLockKey(t *testing.T) {
	lock := &redisPodLock{prefix: "ks"}
	if got := lock.redisKey("default/api"); got != "ks:lock:pod:default/api" {
		t.Fatalf("unexpected redis key: %s", got)
	}
}
