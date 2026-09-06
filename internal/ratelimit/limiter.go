package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrUnavailable = errors.New("rate limiter unavailable")

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

// Limiter is deliberately small so HTTP tests can inject a deterministic
// implementation without running Redis.
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (Decision, error)
	Ping(ctx context.Context) error
}

const script = `
local current = redis.call('INCR', KEYS[1])
if current == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
local ttl = redis.call('PTTL', KEYS[1])
if current > tonumber(ARGV[2]) then
  return {0, ttl}
end
return {1, ttl}
`

var windowScript = redis.NewScript(script)

type RedisLimiter struct {
	client *redis.Client
}

func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (Decision, error) {
	if l == nil || l.client == nil {
		return Decision{}, ErrUnavailable
	}
	if limit < 1 {
		return Decision{}, fmt.Errorf("rate limit must be positive")
	}
	window = boundedWindow(window)
	values, err := windowScript.Run(ctx, l.client, []string{key}, window.Milliseconds(), limit).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("rate limiter: %w", err)
	}
	if len(values) != 2 {
		return Decision{}, fmt.Errorf("rate limiter returned malformed result")
	}
	retryAfter := time.Duration(values[1]) * time.Millisecond
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return Decision{Allowed: values[0] == 1, RetryAfter: retryAfter}, nil
}

func (l *RedisLimiter) Ping(ctx context.Context) error {
	if l == nil || l.client == nil {
		return ErrUnavailable
	}
	if err := l.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("rate limiter ping: %w", err)
	}
	return nil
}

func boundedWindow(window time.Duration) time.Duration {
	if window < time.Second {
		return time.Second
	}
	if window > time.Hour {
		return time.Hour
	}
	return window
}

// Key hashes email and client IP independently. The project ID remains an
// explicit namespace while no raw PII is put into Redis keys.
func Key(operation, projectID, normalizedEmail, clientIP string) string {
	emailHash := sha256.Sum256([]byte(normalizedEmail))
	ipHash := sha256.Sum256([]byte(clientIP))
	return "stealth:ratelimit:v1:" + operation + ":project:" + projectID + ":email:" + hex.EncodeToString(emailHash[:]) + ":ip:" + hex.EncodeToString(ipHash[:])
}

// ProjectIPKey provides a project-scoped aggregate bucket for a client IP.
// It complements Key so rotating email addresses cannot bypass the limit.
func ProjectIPKey(operation, projectID, clientIP string) string {
	ipHash := sha256.Sum256([]byte(clientIP))
	return "stealth:ratelimit:v1:" + operation + ":project:" + projectID + ":ip:" + hex.EncodeToString(ipHash[:])
}

type NoopLimiter struct{}

func (NoopLimiter) Allow(context.Context, string, int, time.Duration) (Decision, error) {
	return Decision{Allowed: true}, nil
}

func (NoopLimiter) Ping(context.Context) error { return nil }

type UnavailableLimiter struct{}

func (UnavailableLimiter) Allow(context.Context, string, int, time.Duration) (Decision, error) {
	return Decision{}, ErrUnavailable
}

func (UnavailableLimiter) Ping(context.Context) error { return ErrUnavailable }

type memoryEntry struct {
	count   int
	expires time.Time
}

// MemoryLimiter is intended for deterministic tests and local development;
// production uses RedisLimiter so limits work across API replicas.
type MemoryLimiter struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	now     func() time.Time
}

func NewMemoryLimiter() *MemoryLimiter {
	return &MemoryLimiter{entries: make(map[string]memoryEntry), now: time.Now}
}

func (l *MemoryLimiter) Allow(_ context.Context, key string, limit int, window time.Duration) (Decision, error) {
	if limit < 1 {
		return Decision{}, fmt.Errorf("rate limit must be positive")
	}
	window = boundedWindow(window)
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || !now.Before(entry.expires) {
		entry = memoryEntry{expires: now.Add(window)}
	}
	entry.count++
	l.entries[key] = entry
	if entry.count > limit {
		return Decision{Allowed: false, RetryAfter: entry.expires.Sub(now)}, nil
	}
	return Decision{Allowed: true, RetryAfter: entry.expires.Sub(now)}, nil
}

func (l *MemoryLimiter) Ping(context.Context) error { return nil }

// SetNow is useful only in package tests; it avoids sleeping to test expiry.
func (l *MemoryLimiter) SetNow(now func() time.Time) { l.now = now }
