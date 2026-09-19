package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/functionsecret"
	"github.com/Stealth-deplover/stealth/internal/repository"
	"github.com/google/uuid"
)

func TestHeartbeatCheckUsesHashedTokenAndGracePeriod(t *testing.T) {
	token := strings.Repeat("a", 32)
	hash := sha256.Sum256([]byte(token))
	config, err := json.Marshal(map[string]any{
		"token_hash":    hex.EncodeToString(hash[:]),
		"grace_seconds": 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := functionsecret.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	job := repository.AdminMonitorJob{
		ID:              uuid.Must(uuid.NewV7()),
		Kind:            "heartbeat",
		Target:          "nightly-backup",
		IntervalSeconds: 60,
		TimeoutMS:       1000,
		EncryptedConfig: encrypted,
		LastHeartbeatAt: timePtr(now.Add(-time.Minute)),
	}
	result := Check(context.Background(), job, cipher)
	if !result.Success || result.Error != "" {
		t.Fatalf("fresh heartbeat result = %+v", result)
	}

	job.LastHeartbeatAt = timePtr(now.Add(-2 * time.Minute))
	result = Check(context.Background(), job, cipher)
	if result.Success || result.Error != "heartbeat is late" {
		t.Fatalf("late heartbeat result = %+v", result)
	}
	if strings.Contains(result.Error, token) || strings.Contains(string(result.Details), token) {
		t.Fatalf("heartbeat token leaked: %+v", result)
	}
}

func TestCheckRejectsPrivateHTTPTargetBeforeNetworkAccess(t *testing.T) {
	config, err := json.Marshal(map[string]any{"method": "GET", "expected_status": 200})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := functionsecret.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt(config)
	if err != nil {
		t.Fatal(err)
	}
	result := Check(context.Background(), repository.AdminMonitorJob{
		ID: uuid.Must(uuid.NewV7()), Kind: "http", Target: "http://127.0.0.1:8080/healthz", TimeoutMS: 1000,
		EncryptedConfig: encrypted,
	}, cipher)
	if result.Success || result.Error != "monitor target resolves to a private address" {
		t.Fatalf("private target result = %+v", result)
	}
}

func timePtr(value time.Time) *time.Time { return &value }
