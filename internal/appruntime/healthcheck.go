package appruntime

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Stealth-deplover/stealth/internal/workloadspec"
)

var (
	ErrHealthRuntimeDrift = errors.New("App health runtime identity changed")
	ErrHealthProbeFailed  = errors.New("App health probe failed")
)

const healthResponseBodyLimit = 4096

// runHealthProbe performs bounded TCP or HTTP checks against one private
// address already verified from the current managed container inspection.
func runHealthProbe(parent context.Context, address string, spec workloadspec.Spec) error {
	if !validPrivateProbeAddress(address) {
		return ErrHealthRuntimeDrift
	}
	normalized, err := workloadspec.Normalize(spec)
	if err != nil {
		return ErrHealthRuntimeDrift
	}
	return runNormalizedHealthProbe(parent, address, normalized, &net.Dialer{})
}

type healthProbeDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

func runNormalizedHealthProbe(parent context.Context, address string, spec workloadspec.Spec, dialer healthProbeDialer) error {
	timeout := time.Duration(spec.HealthCheck.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	endpoint := net.JoinHostPort(address, strconv.Itoa(spec.Port))
	switch spec.HealthCheck.Protocol {
	case "tcp":
		connection, err := dialer.DialContext(ctx, "tcp", endpoint)
		if err != nil {
			return ErrHealthProbeFailed
		}
		_ = connection.Close()
		return nil
	case "http":
		path := spec.HealthCheck.Path
		if path == nil {
			return ErrHealthRuntimeDrift
		}
		target, err := url.ParseRequestURI(*path)
		if err != nil || target.IsAbs() || target.Host != "" || target.User != nil {
			return ErrHealthRuntimeDrift
		}
		target.Scheme = "http"
		target.Host = endpoint
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return ErrHealthRuntimeDrift
		}
		request.Header.Set("User-Agent", "Stealth-App-Health/1")
		client := &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				Proxy: nil, DisableKeepAlives: true, DisableCompression: true,
				MaxConnsPerHost: 1, ResponseHeaderTimeout: timeout,
				DialContext: dialer.DialContext,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		response, err := client.Do(request)
		if err != nil {
			return ErrHealthProbeFailed
		}
		defer response.Body.Close()
		// A 2xx response is the documented healthy class. Redirects, even to a
		// local address, are not followed; the bounded drain avoids retaining
		// connections or reading an unbounded tenant response body.
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, healthResponseBodyLimit))
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return ErrHealthProbeFailed
		}
		return nil
	default:
		return ErrHealthRuntimeDrift
	}
}

func validPrivateProbeAddress(value string) bool {
	address := net.ParseIP(value)
	return address != nil && address.To4() != nil && address.IsPrivate() && !address.IsLoopback() &&
		!address.IsUnspecified() && !address.IsLinkLocalUnicast() && !address.IsMulticast()
}
