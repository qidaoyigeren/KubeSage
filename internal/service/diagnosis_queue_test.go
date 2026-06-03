package service

import (
	"context"
	"testing"
	"time"

	"kubesage/internal/config"
)

func TestQueueRetryReacquiresPodLockBeforeExecution(t *testing.T) {
	lock := &localPodLock{entries: map[string]time.Time{}}
	lockKey := podLockKey("default", "api-0")
	originalToken, ok, err := lock.TryAcquire(context.Background(), lockKey, time.Minute)
	if err != nil || !ok {
		t.Fatal("initial lock acquire should succeed")
	}
	svc := &DiagnosisService{
		podLocks: lock,
		cfg:      &config.Config{Diagnosis: config.DiagnosisConfig{PodLockTTLSeconds: 60}},
	}
	q := &redisDiagnosisQueue{}
	payload := queuedDiagnosis{
		TaskID:    1,
		Request:   PodDiagnosisRequest{Namespace: "default", PodName: "api-0"},
		LockKey:   lockKey,
		LockToken: "stale-token",
		Attempts:  1,
	}

	if _, acquired := q.acquireExecutionLock(context.Background(), svc, payload); acquired {
		t.Fatal("retry should wait when pod lock is still held")
	}
	if err := lock.Release(context.Background(), lockKey, originalToken); err != nil {
		t.Fatal(err)
	}
	next, acquired := q.acquireExecutionLock(context.Background(), svc, payload)
	if !acquired {
		t.Fatal("retry should acquire lock after previous attempt released it")
	}
	if next.LockToken == "stale-token" {
		t.Fatal("retry should replace stale lock token before execution")
	}
}
