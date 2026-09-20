package monitoring

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
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

func TestMonitorPublicAddressPolicyRejectsSpecialUseAndMappedPrivateIPs(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		public bool
	}{
		{name: "public IPv4", value: "8.8.8.8", public: true},
		{name: "public IPv6", value: "2001:4860:4860::8888", public: true},
		{name: "public IPv4 mapped IPv6", value: "::ffff:8.8.8.8", public: true},
		{name: "this network", value: "0.0.0.1"},
		{name: "private IPv4", value: "10.1.2.3"},
		{name: "shared address space", value: "100.64.0.1"},
		{name: "loopback IPv4", value: "127.0.0.1"},
		{name: "metadata link local", value: "169.254.169.254"},
		{name: "private IPv4", value: "172.31.255.254"},
		{name: "IETF assignment", value: "192.0.0.9"},
		{name: "documentation IPv4", value: "192.0.2.1"},
		{name: "AS112 IPv4", value: "192.31.196.1"},
		{name: "AMT IPv4", value: "192.52.193.1"},
		{name: "deprecated 6to4 anycast", value: "192.88.99.1"},
		{name: "private IPv4", value: "192.168.1.1"},
		{name: "AS112 IPv4", value: "192.175.48.1"},
		{name: "benchmarking IPv4", value: "198.18.0.1"},
		{name: "documentation IPv4", value: "198.51.100.1"},
		{name: "documentation IPv4", value: "203.0.113.1"},
		{name: "multicast IPv4", value: "224.0.0.1"},
		{name: "reserved IPv4", value: "240.0.0.1"},
		{name: "unspecified IPv6", value: "::"},
		{name: "loopback IPv6", value: "::1"},
		{name: "mapped loopback", value: "::ffff:127.0.0.1"},
		{name: "mapped shared address", value: "::ffff:100.64.0.1"},
		{name: "discard-only IPv6", value: "100::1"},
		{name: "dummy IPv6", value: "100:0:0:1::1"},
		{name: "Teredo", value: "2001:0::1"},
		{name: "benchmarking IPv6", value: "2001:2::1"},
		{name: "ORCHID", value: "2001:10::1"},
		{name: "ORCHIDv2", value: "2001:20::1"},
		{name: "documentation IPv6", value: "2001:db8::1"},
		{name: "6to4 IPv6", value: "2002::1"},
		{name: "AS112 IPv6", value: "2620:4f:8000::1"},
		{name: "new documentation IPv6", value: "3fff::1"},
		{name: "SRv6 SID", value: "5f00::1"},
		{name: "unique local IPv6", value: "fc00::1"},
		{name: "link local IPv6", value: "fe80::1"},
		{name: "multicast IPv6", value: "ff02::1"},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+test.value, func(t *testing.T) {
			ip := net.ParseIP(test.value)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) returned nil", test.value)
			}
			if got := isPublicIP(ip); got != test.public {
				t.Fatalf("isPublicIP(%q) = %v, want %v", test.value, got, test.public)
			}
		})
	}
}

func TestResolvePublicHostRejectsMixedDNSAnswers(t *testing.T) {
	resolver := testIPResolver(func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.10")}, nil
	})
	if _, err := resolvePublicHostWithResolver(context.Background(), resolver, "mixed.example.test"); err == nil {
		t.Fatal("resolvePublicHostWithResolver accepted mixed public/private DNS answers")
	}
}

func TestMonitorURLValidationAllowsQueriesAndRejectsUnsafeComponents(t *testing.T) {
	resolver := testIPResolver(func(context.Context, string, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("8.8.8.8")}, nil
	})
	tests := []struct {
		name   string
		target string
		valid  bool
	}{
		{name: "query is valid", target: "https://example.test/health?ready=1", valid: true},
		{name: "empty query is valid", target: "https://example.test/health?", valid: true},
		{name: "userinfo is rejected", target: "https://user:pass@example.test/health"},
		{name: "fragment is rejected", target: "https://example.test/health#ready"},
		{name: "wrong scheme is rejected", target: "ftp://example.test/health"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parsed, err := url.Parse(test.target)
			if err != nil {
				t.Fatal(err)
			}
			err = validatePublicURLWithResolver(context.Background(), resolver, parsed)
			if (err == nil) != test.valid {
				t.Fatalf("validatePublicURLWithResolver(%q) error=%v, valid=%v", test.target, err, test.valid)
			}
		})
	}

	if err := ValidateConfig("http", "https://example.test/health?ready=1", []byte(`{"method":"GET","expected_status":200}`)); err != nil {
		t.Fatalf("ValidateConfig rejected a valid query-string monitor: %v", err)
	}
}

func TestMonitorURLValidationHonorsResolverCancellationAndDeadline(t *testing.T) {
	tests := []struct {
		name    string
		cancel  func(context.Context, context.CancelFunc)
		wantErr error
	}{
		{
			name: "cancellation",
			cancel: func(_ context.Context, cancel context.CancelFunc) {
				cancel()
			},
			wantErr: context.Canceled,
		},
		{
			name: "deadline",
			cancel: func(ctx context.Context, _ context.CancelFunc) {
				<-ctx.Done()
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			started := make(chan struct{})
			resolver := testIPResolver(func(ctx context.Context, _, _ string) ([]net.IP, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			})
			parsed, err := url.Parse("https://slow.example.test/health")
			if err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				result <- validatePublicURLWithResolver(ctx, resolver, parsed)
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("resolver was not called")
			}
			if test.name == "cancellation" {
				cancel()
			}
			select {
			case err := <-result:
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("validation error = %v, want %v", err, test.wantErr)
				}
			case <-time.After(time.Second):
				t.Fatal("validation did not stop after context termination")
			}
		})
	}
}

func TestExpectedDNSValuesUseDocumentedSubsetSemantics(t *testing.T) {
	tests := []struct {
		name     string
		actual   []string
		expected []string
		valid    bool
	}{
		{name: "all expected values present", actual: []string{"1.1.1.1", "2.2.2.2"}, expected: []string{"1.1.1.1", "2.2.2.2"}, valid: true},
		{name: "additional records allowed", actual: []string{"1.1.1.1", "2.2.2.2"}, expected: []string{"1.1.1.1"}, valid: true},
		{name: "missing expected value rejected", actual: []string{"1.1.1.1"}, expected: []string{"1.1.1.1", "2.2.2.2"}},
		{name: "trailing DNS dot normalized", actual: []string{"edge.example.test."}, expected: []string{"edge.example.test"}, valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := expectedDNSValuesPresent(test.actual, test.expected); got != test.valid {
				t.Fatalf("expectedDNSValuesPresent(%v, %v) = %v, want %v", test.actual, test.expected, got, test.valid)
			}
		})
	}
}

type testIPResolver func(context.Context, string, string) ([]net.IP, error)

func (resolver testIPResolver) LookupIP(ctx context.Context, network, host string) ([]net.IP, error) {
	return resolver(ctx, network, host)
}

func timePtr(value time.Time) *time.Time { return &value }
