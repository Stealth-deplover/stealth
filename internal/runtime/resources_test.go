package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Stealth-deplover/stealth/internal/config"
)

func TestOpenRejectsMalformedRedisBeforeOpeningDatabase(t *testing.T) {
	_, err := Open(context.Background(), config.Config{
		DatabaseURL: "postgres://user:pass@localhost:5432/stealth",
		RedisURL:    "not-a-redis-url",
	}, OpenOptions{WithRedis: true})
	if err == nil || !strings.Contains(err.Error(), "Redis") {
		t.Fatalf("Open error = %v, want Redis configuration error", err)
	}
}

func TestOpenRejectsMalformedDatabaseConfiguration(t *testing.T) {
	_, err := Open(context.Background(), config.Config{DatabaseURL: "not-a-database-url"}, OpenOptions{})
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("Open error = %v, want database configuration error", err)
	}
}

func TestResourcesCloseIsNilSafe(t *testing.T) {
	var resources *Resources
	resources.Close()
}
