package appruntime

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Stealth-deplover/stealth/internal/workloadspec"
)

type healthTestDialer func(context.Context, string, string) (net.Conn, error)

func (d healthTestDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d(ctx, network, address)
}

func TestRunHealthProbeRestrictsDestinationsToPrivateRuntimeAddresses(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "169.254.169.254", "203.0.113.8", "::1", "not-an-ip"} {
		t.Run(address, func(t *testing.T) {
			if err := runHealthProbe(context.Background(), address, workloadspec.Default()); !errors.Is(err, ErrHealthRuntimeDrift) {
				t.Fatalf("probe error = %v, want runtime drift", err)
			}
		})
	}
}

func TestRunHealthProbeTCPUsesOnlyConfiguredContainerPort(t *testing.T) {
	spec := workloadspec.Default()
	spec.Port = 8123
	spec.HealthCheck.TimeoutSeconds = 1
	called := false
	dialer := healthTestDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
		called = true
		if network != "tcp" || address != net.JoinHostPort("172.22.0.5", strconv.Itoa(spec.Port)) {
			t.Fatalf("dial target = %s %s", network, address)
		}
		client, server := net.Pipe()
		_ = server.Close()
		return client, nil
	})
	if err := runNormalizedHealthProbe(context.Background(), "172.22.0.5", spec, dialer); err != nil {
		t.Fatalf("TCP probe: %v", err)
	}
	if !called {
		t.Fatal("TCP probe did not connect")
	}
}

func TestRunHealthProbeHTTP2xxIsHealthyAndUsesValidatedLocalPath(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.RequestURI()
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	serverAddress := strings.TrimPrefix(server.URL, "http://")
	spec := httpHealthSpec("/ready")
	var targets []string
	dialer := healthTestDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
		targets = append(targets, address)
		if network != "tcp" || address != net.JoinHostPort("172.22.0.5", strconv.Itoa(spec.Port)) {
			t.Fatalf("health request dialed unexpected target %s %s", network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", serverAddress)
	})
	if err := runNormalizedHealthProbe(context.Background(), "172.22.0.5", spec, dialer); err != nil {
		t.Fatalf("HTTP probe: %v", err)
	}
	if gotPath != "/ready" || len(targets) != 1 {
		t.Fatalf("request path=%q dial targets=%v", gotPath, targets)
	}
}

func TestRunHealthProbeHTTPRedirectIsNotFollowed(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		http.Redirect(writer, request, "http://169.254.169.254/latest/meta-data", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	serverAddress := strings.TrimPrefix(server.URL, "http://")
	spec := httpHealthSpec("/healthz")
	var targets []string
	dialer := healthTestDialer(func(ctx context.Context, network, address string) (net.Conn, error) {
		targets = append(targets, address)
		return (&net.Dialer{}).DialContext(ctx, "tcp", serverAddress)
	})
	if err := runNormalizedHealthProbe(context.Background(), "172.22.0.5", spec, dialer); !errors.Is(err, ErrHealthProbeFailed) {
		t.Fatalf("redirect probe error = %v, want unhealthy probe", err)
	}
	if requests != 1 || len(targets) != 1 {
		t.Fatalf("redirect was followed: requests=%d dial targets=%v", requests, targets)
	}
}

func TestRunHealthProbeHTTPOnlyTreats2xxAsHealthy(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusMultipleChoices, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(status)
				_, _ = writer.Write([]byte(strings.Repeat("x", healthResponseBodyLimit*3)))
			}))
			defer server.Close()
			serverAddress := strings.TrimPrefix(server.URL, "http://")
			dialer := healthTestDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", serverAddress)
			})
			err := runNormalizedHealthProbe(context.Background(), "172.22.0.5", httpHealthSpec("/healthz"), dialer)
			if status >= http.StatusOK && status < http.StatusMultipleChoices {
				if err != nil {
					t.Fatalf("2xx response was unhealthy: %v", err)
				}
			} else if !errors.Is(err, ErrHealthProbeFailed) {
				t.Fatalf("status %d error = %v, want probe failure", status, err)
			}
		})
	}
}

func TestRunHealthProbeHonorsConfiguredTimeout(t *testing.T) {
	spec := httpHealthSpec("/healthz")
	spec.HealthCheck.TimeoutSeconds = 1
	started := time.Now()
	dialer := healthTestDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err := runNormalizedHealthProbe(context.Background(), "172.22.0.5", spec, dialer); !errors.Is(err, ErrHealthProbeFailed) {
		t.Fatalf("timed out probe error = %v, want probe failure", err)
	}
	if time.Since(started) > 1500*time.Millisecond {
		t.Fatalf("probe exceeded configured timeout: %s", time.Since(started))
	}
}

func httpHealthSpec(path string) workloadspec.Spec {
	spec := workloadspec.Default()
	spec.Port = 8081
	spec.HealthCheck.Protocol = "http"
	spec.HealthCheck.Path = &path
	spec, _ = workloadspec.Normalize(spec)
	return spec
}
