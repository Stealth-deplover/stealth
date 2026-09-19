// Package collectorhealthcheck probes the live HTTP health endpoint exposed by
// the OpenTelemetry Collector health_check extension.
package collectorhealthcheck

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Check returns nil only when endpoint responds with HTTP 200. The probe is
// intentionally small so it can run from the scratch-based Collector image
// without adding a shell or a general-purpose HTTP client.
func Check(ctx context.Context, endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "http://127.0.0.1:13133/"
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		if err == nil {
			err = fmt.Errorf("endpoint must be an http URL")
		}
		return fmt.Errorf("invalid Collector health endpoint: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fmt.Errorf("create Collector health request: %w", err)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		return fmt.Errorf("Collector health request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Collector health status: %s", response.Status)
	}
	return nil
}
