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

// Key derives deterministic Redis namespace components from the email and
// client IP. These are not passwords or authentication secrets: the digest is
// used only to keep raw PII out of Redis keys and to make equivalent requests
// share the same rate-limit bucket.
func Key(operation, projectID, normalizedEmail, clientIP string) string {
	return "stealth:ratelimit:v1:" + operation + ":project:" + projectID + ":email:" + redisNamespaceDigest(normalizedEmail) + ":ip:" + redisNamespaceDigest(clientIP)
}

// ProjectIPKey provides a project-scoped aggregate bucket for a client IP.
// It complements Key so rotating email addresses cannot bypass the limit.
func ProjectIPKey(operation, projectID, clientIP string) string {
	return "stealth:ratelimit:v1:" + operation + ":project:" + projectID + ":ip:" + redisNamespaceDigest(clientIP)
}

// InstanceIPKey keeps instance bootstrap attempts in their own namespace.
// The literal scope prevents a setup attack from sharing buckets with tenant
// project or Console account authentication.
func InstanceIPKey(operation, clientIP string) string {
	return ProjectIPKey(operation, "instance", clientIP)
}

// InstanceKey scopes a bootstrap email/IP bucket to the installation rather
// than exposing the raw email address in Redis.
func InstanceKey(operation, normalizedEmail, clientIP string) string {
	return Key(operation, "instance", normalizedEmail, clientIP)
}

// ActorKey scopes an operation to a stable authenticated actor and the
// project/resource scope supplied by the caller. It deliberately has no IP
// dimension: an authenticated actor keeps one budget when their network
// changes. Use ProjectIPKey separately when a public/shared-source budget is
// also needed.
func ActorKey(operation, scope, actorID string) string {
	return "stealth:ratelimit:v1:" + operation + ":scope:" + scope + ":actor:" + redisNamespaceDigest(actorID)
}

// redisNamespaceDigest is deliberately a fast, deterministic digest for a
// non-password Redis key component. Passwords and other authentication
// secrets must use their dedicated slow password hashing or encryption
// primitives instead.
func redisNamespaceDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
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
