package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"kubesage/internal/config"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type diagnosisLock interface {
	TryAcquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error)
	Release(ctx context.Context, key, token string) error
}

type localPodLock struct {
	mu      sync.Mutex
	entries map[string]time.Time
}

type redisPodLock struct {
	client redis.UniversalClient
	prefix string
}

var releaseRedisLockScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

// newDiagnosisLock prefers Redis for cross-instance locking and falls back to
// an in-process lock when Redis is not enabled.
func newDiagnosisLock(cfg config.RedisConfig, client redis.UniversalClient) diagnosisLock {
	if cfg.Enabled && client != nil {
		prefix := strings.TrimSpace(cfg.KeyPrefix)
		if prefix == "" {
			prefix = "kubesage"
		}
		return &redisPodLock{client: client, prefix: prefix}
	}
	return &localPodLock{entries: map[string]time.Time{}}
}

// TryAcquire locks a pod key locally until ttl expires or Release is called.
func (l *localPodLock) TryAcquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	now := time.Now()
	expiresAt := now.Add(ttl)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.deleteExpired(now)
	if existing, ok := l.entries[key]; ok && existing.After(now) {
		return "", false, nil
	}
	l.entries[key] = expiresAt
	return "local", true, nil
}

// Release unlocks a local pod key when a diagnosis worker exits.
func (l *localPodLock) Release(ctx context.Context, key, token string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
	return nil
}

// deleteExpired removes stale local lock entries opportunistically.
func (l *localPodLock) deleteExpired(now time.Time) {
	for key, expiresAt := range l.entries {
		if !expiresAt.After(now) {
			delete(l.entries, key)
		}
	}
}

// TryAcquire uses Redis SET NX with a unique token for distributed locking.
func (l *redisPodLock) TryAcquire(ctx context.Context, key string, ttl time.Duration) (string, bool, error) {
	if l == nil || l.client == nil {
		return "", false, fmt.Errorf("redis lock client is not configured")
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	token := uuid.NewString()
	ok, err := l.client.SetNX(ctx, l.redisKey(key), token, ttl).Result()
	if err != nil {
		return "", false, err
	}
	return token, ok, nil
}

// Release removes the Redis lock only when the stored token matches.
func (l *redisPodLock) Release(ctx context.Context, key, token string) error {
	if l == nil || l.client == nil || token == "" {
		return nil
	}
	return releaseRedisLockScript.Run(ctx, l.client, []string{l.redisKey(key)}, token).Err()
}

// redisKey scopes diagnosis locks under a configurable prefix.
func (l *redisPodLock) redisKey(key string) string {
	return strings.TrimRight(l.prefix, ":") + ":lock:pod:" + key
}
