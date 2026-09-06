package ratelimit

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMemoryLimiterEnforcesWindowAndResets(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := NewMemoryLimiter()
	limiter.SetNow(func() time.Time { return now })

	if decision, err := limiter.Allow(context.Background(), "key", 2, time.Minute); err != nil || !decision.Allowed {
		t.Fatalf("first request: %+v, %v", decision, err)
	}
	if decision, err := limiter.Allow(context.Background(), "key", 2, time.Minute); err != nil || !decision.Allowed {
		t.Fatalf("second request: %+v, %v", decision, err)
	}
	if decision, err := limiter.Allow(context.Background(), "key", 2, time.Minute); err != nil || decision.Allowed || decision.RetryAfter != time.Minute {
		t.Fatalf("third request: %+v, %v", decision, err)
	}
	now = now.Add(time.Minute)
	if decision, err := limiter.Allow(context.Background(), "key", 2, time.Minute); err != nil || !decision.Allowed {
		t.Fatalf("request after expiry: %+v, %v", decision, err)
	}
}

func TestKeyDoesNotContainRawEmailOrIP(t *testing.T) {
	key := Key("login", "018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", "person@example.test", "203.0.113.10")
	if strings.Contains(key, "person@example.test") || strings.Contains(key, "203.0.113.10") {
		t.Fatalf("raw identity data leaked into limiter key: %s", key)
	}
	if !strings.HasPrefix(key, "stealth:ratelimit:v1:login:project:") {
		t.Fatalf("unexpected limiter key prefix: %s", key)
	}
	ipKey := ProjectIPKey("login", "018f27e3-5d1a-7c44-ae35-1db4ea12e6d2", "203.0.113.10")
	if strings.Contains(ipKey, "person@example.test") || strings.Contains(ipKey, "203.0.113.10") {
		t.Fatalf("raw identity data leaked into project IP key: %s", ipKey)
	}
}

func TestMemoryLimiterSupportsAggregateAndEmailBuckets(t *testing.T) {
	limiter := NewMemoryLimiter()
	window := 10 * time.Second
	for _, key := range []string{
		ProjectIPKey("login", "project-1", "198.51.100.4"),
		Key("login", "project-1", "person@example.com", "198.51.100.4"),
	} {
		if decision, err := limiter.Allow(context.Background(), key, 1, window); err != nil || !decision.Allowed {
			t.Fatalf("first request for %q was not allowed: %+v %v", key, decision, err)
		}
		decision, err := limiter.Allow(context.Background(), key, 1, window)
		if err != nil || decision.Allowed || decision.RetryAfter <= 0 {
			t.Fatalf("second request for %q did not return a bounded retry: %+v %v", key, decision, err)
		}
	}
}

func TestUnavailableLimiterFailsClosed(t *testing.T) {
	if _, err := (UnavailableLimiter{}).Allow(context.Background(), "key", 1, time.Minute); err != ErrUnavailable {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}
