package webhookrunner

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/nazxf/stealth-api/internal/functionsecret"
	"github.com/nazxf/stealth-api/internal/repository"
)

func TestBlockedAddress(t *testing.T) {
	for _, value := range []string{"127.0.0.1", "::1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "0.0.0.0"} {
		address, err := netip.ParseAddr(value)
		if err != nil {
			t.Fatal(err)
		}
		if !blockedAddress(address) {
			t.Errorf("blockedAddress(%s) = false", value)
		}
	}
	public, err := netip.ParseAddr("8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}
	if blockedAddress(public) {
		t.Fatal("public address was blocked")
	}
}

func TestRetryHelpers(t *testing.T) {
	if !retryableStatus(429) || !retryableStatus(503) || retryableStatus(400) {
		t.Fatal("unexpected retry status classification")
	}
	if got := parseRetryAfter("10"); got != 10*time.Second {
		t.Fatalf("Retry-After = %v", got)
	}
	if got := parseRetryAfter("999999999"); got != maxRetryDelay {
		t.Fatalf("Retry-After cap = %v", got)
	}
	if got := parseRetryAfter("Wed, 21 Oct 2015 07:28:00 GMT"); got != 0 {
		t.Fatalf("date Retry-After unexpectedly accepted: %v", got)
	}
	worker := &Worker{}
	first := worker.retryAt(1, 0)
	if first.Before(time.Now().Add(23*time.Second)) || first.After(time.Now().Add(37*time.Second)) {
		t.Fatalf("first retry at %v", first)
	}
	if got := worker.retryAt(99, 0).Sub(time.Now()); got > maxRetryDelay || got < (maxRetryDelay*4)/5-time.Second {
		t.Fatalf("retry cap = %v", got)
	}
}

func TestSignatureMessage(t *testing.T) {
	secret := []byte("whsec_test")
	timestamp := "1725273600"
	body := []byte(`{"event":"project.create"}`)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	want := "c227e8db73326c321bbcdfe838dcef5f49745936043d6e509ad742a7f9c7c349"
	if got := hex.EncodeToString(mac.Sum(nil)); got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

func TestWorkerConstruction(t *testing.T) {
	cipher, err := functionsecret.New(make([]byte, functionsecret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(&repository.Repository{}, cipher, "worker-1", nil); err != nil {
		t.Fatal(err)
	}
	for _, workerID := range []string{"", " worker", "../worker"} {
		if _, err := New(&repository.Repository{}, cipher, workerID, nil); err == nil {
			t.Errorf("worker ID %q unexpectedly accepted", workerID)
		}
	}
	if !strings.HasPrefix(safeError(context.Canceled), "context canceled") {
		t.Fatal("safeError did not preserve bounded context error")
	}
}

func TestSafeDialContextRejectsPrivateDestinations(t *testing.T) {
	for _, address := range []string{"127.0.0.1:443", "[::1]:443", "10.0.0.1:443", "169.254.169.254:80"} {
		if connection, err := safeDialContext(context.Background(), "tcp", address); err == nil || connection != nil {
			t.Fatalf("safeDialContext(%q) = connection=%v, error=%v", address, connection, err)
		}
	}
}
