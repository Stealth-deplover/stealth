// Package dockermetricsproxy exposes the small, read-only Docker API surface
// required by the OpenTelemetry docker_stats receiver. It is deliberately
// separate from the worker, which is the only other production service that
// currently needs Docker mutation authority.
package dockermetricsproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	defaultListenAddress = "0.0.0.0:2375"
	defaultSocketPath    = "/var/run/docker.sock"
	maxJSONResponse      = 16 << 20
)

var containerIDPattern = regexp.MustCompile(`^[a-fA-F0-9]{12,64}$`)
var apiVersionPattern = regexp.MustCompile(`^/v[0-9]+(?:\.[0-9]+)?(/.*)$`)

// Config controls the internal proxy listener and its upstream Docker socket.
type Config struct {
	ListenAddress string
	SocketPath    string
}

// NewHandler returns the proxy handler. It does not listen or mutate process
// state, which keeps policy and sanitization straightforward to test.
func NewHandler(cfg Config) http.Handler {
	if strings.TrimSpace(cfg.SocketPath) == "" {
		cfg.SocketPath = defaultSocketPath
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     false,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       30 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", cfg.SocketPath)
		},
	}
	return &handler{
		client: &http.Client{Transport: transport, Timeout: 30 * time.Second},
	}
}

// Run serves the proxy until ctx is cancelled. The listener is intentionally
// bound only to the Compose network; production Compose does not publish it.
func Run(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.ListenAddress) == "" {
		cfg.ListenAddress = defaultListenAddress
	}
	listener, err := net.Listen("tcp", cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("listen for Docker metrics proxy: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler:           NewHandler(cfg),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve Docker metrics proxy: %w", err)
	}
	return nil
}

// Healthcheck checks both the listener and the Docker socket. It is used by
// the image healthcheck and therefore does not need a shell in the image.
func Healthcheck(ctx context.Context, address string) error {
	if strings.TrimSpace(address) == "" {
		address = "127.0.0.1:2375"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/healthz", nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("Docker metrics proxy health request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Docker metrics proxy health status: %s", response.Status)
	}
	return nil
}

type handler struct {
	client *http.Client
}

func (h *handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		h.serveHealth(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "Docker metrics proxy permits read-only requests", http.StatusMethodNotAllowed)
		return
	}
	path, kind, ok := allowedPath(request.URL.Path)
	if !ok || !allowedQuery(kind, request.URL.Query()) {
		http.Error(writer, "Docker API endpoint is not permitted", http.StatusForbidden)
		return
	}

	upstreamURL := &url.URL{Scheme: "http", Host: "docker", Path: path, RawQuery: request.URL.RawQuery}
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, upstreamURL.String(), nil)
	if err != nil {
		http.Error(writer, "could not create Docker request", http.StatusBadGateway)
		return
	}
	response, err := h.client.Do(upstreamRequest)
	if err != nil {
		http.Error(writer, "Docker daemon is unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	if kind == endpointInspect || kind == endpointContainers {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxJSONResponse+1))
		if readErr != nil || len(body) > maxJSONResponse {
			http.Error(writer, "Docker response is too large", http.StatusBadGateway)
			return
		}
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			var sanitized []byte
			if kind == endpointInspect {
				sanitized, err = sanitizeInspect(body)
			} else {
				sanitized, err = sanitizeContainers(body)
			}
			if err != nil {
				http.Error(writer, "Docker response could not be validated", http.StatusBadGateway)
				return
			}
			body = sanitized
		}
		copyHeaders(writer.Header(), response.Header)
		writer.Header().Del("Content-Length")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(response.StatusCode)
		if request.Method != http.MethodHead {
			_, _ = writer.Write(body)
		}
		return
	}

	copyHeaders(writer.Header(), response.Header)
	writer.WriteHeader(response.StatusCode)
	if request.Method != http.MethodHead {
		if kind == endpointEvents {
			copyEventStream(writer, response.Body)
		} else {
			_, _ = io.CopyN(writer, response.Body, maxJSONResponse)
		}
	}
}

func copyEventStream(writer http.ResponseWriter, reader io.Reader) {
	buffer := make([]byte, 32<<10)
	flusher, canFlush := writer.(http.Flusher)
	for {
		read, err := reader.Read(buffer)
		if read > 0 {
			if _, writeErr := writer.Write(buffer[:read]); writeErr != nil {
				return
			}
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *handler) serveHealth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 3*time.Second)
	defer cancel()
	upstreamRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/_ping", nil)
	if err != nil {
		http.Error(writer, "unhealthy", http.StatusServiceUnavailable)
		return
	}
	response, err := h.client.Do(upstreamRequest)
	if err != nil {
		http.Error(writer, "Docker daemon is unavailable", http.StatusServiceUnavailable)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(writer, "Docker daemon is unavailable", http.StatusServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = io.WriteString(writer, "ok\n")
	}
}

type endpointKind uint8

const (
	endpointPing endpointKind = iota
	endpointVersion
	endpointEvents
	endpointContainers
	endpointInspect
	endpointStats
)

func allowedPath(rawPath string) (string, endpointKind, bool) {
	path := rawPath
	if matches := apiVersionPattern.FindStringSubmatch(path); matches != nil {
		path = matches[1]
	}
	switch path {
	case "/_ping":
		return rawPath, endpointPing, true
	case "/version":
		return rawPath, endpointVersion, true
	case "/events":
		return rawPath, endpointEvents, true
	case "/containers/json":
		return rawPath, endpointContainers, true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 3 && parts[0] == "containers" && containerIDPattern.MatchString(parts[1]) {
		switch parts[2] {
		case "json":
			return rawPath, endpointInspect, true
		case "stats":
			return rawPath, endpointStats, true
		}
	}
	return "", 0, false
}

func allowedQuery(kind endpointKind, query url.Values) bool {
	allowed := map[endpointKind]map[string]struct{}{
		endpointPing:       {},
		endpointVersion:    {},
		endpointEvents:     {"since": {}, "until": {}, "filters": {}},
		endpointContainers: {"all": {}, "limit": {}, "size": {}, "filters": {}},
		endpointInspect:    {"size": {}, "platform": {}, "checkpoints": {}, "timeout": {}},
		endpointStats:      {"stream": {}, "one-shot": {}},
	}
	for key := range query {
		if _, ok := allowed[kind][key]; !ok {
			return false
		}
	}
	return true
}

func copyHeaders(destination, source http.Header) {
	for key, values := range source {
		if strings.EqualFold(key, "Connection") || strings.EqualFold(key, "Keep-Alive") || strings.EqualFold(key, "Proxy-Authenticate") || strings.EqualFold(key, "Proxy-Authorization") || strings.EqualFold(key, "TE") || strings.EqualFold(key, "Trailer") || strings.EqualFold(key, "Transfer-Encoding") || strings.EqualFold(key, "Upgrade") {
			continue
		}
		for _, value := range values {
			destination.Add(key, value)
		}
	}
}

var safeDockerLabels = map[string]struct{}{
	"com.docker.compose.project":          {},
	"com.docker.compose.service":          {},
	"com.docker.compose.version":          {},
	"com.docker.compose.container-number": {},
}

func sanitizeContainers(body []byte) ([]byte, error) {
	var source []map[string]json.RawMessage
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	containers := make([]map[string]json.RawMessage, 0, len(source))
	for _, container := range source {
		filtered := make(map[string]json.RawMessage)
		copyRawFields(filtered, container, "Id", "Names", "Image", "ImageID", "Created", "State", "Status")
		if labels, ok := container["Labels"]; ok {
			filtered["Labels"] = sanitizeLabels(labels)
		} else {
			filtered["Labels"] = json.RawMessage(`{}`)
		}
		containers = append(containers, filtered)
	}
	return json.Marshal(containers)
}

func sanitizeInspect(body []byte) ([]byte, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	result := make(map[string]json.RawMessage)
	copyRawFields(result, source, "Id", "Name", "Image", "RestartCount")
	if config, ok := object(source["Config"]); ok {
		filtered := make(map[string]json.RawMessage)
		copyRawFields(filtered, config, "Hostname", "Image")
		if labels, ok := config["Labels"]; ok {
			filtered["Labels"] = sanitizeLabels(labels)
		} else {
			filtered["Labels"] = json.RawMessage(`{}`)
		}
		// The receiver only needs command-line shape and configured labels. Do
		// not forward command arguments or environment values from containers.
		filtered["Cmd"] = json.RawMessage(`[]`)
		filtered["Env"] = json.RawMessage(`[]`)
		result["Config"], _ = json.Marshal(filtered)
	}
	if state, ok := object(source["State"]); ok {
		filtered := make(map[string]json.RawMessage)
		copyRawFields(filtered, state, "Status", "Running", "Paused", "StartedAt")
		if health, ok := object(state["Health"]); ok {
			healthResult := make(map[string]json.RawMessage)
			copyRawFields(healthResult, health, "Status")
			filtered["Health"], _ = json.Marshal(healthResult)
		}
		result["State"], _ = json.Marshal(filtered)
	}
	if hostConfig, ok := object(source["HostConfig"]); ok {
		filtered := make(map[string]json.RawMessage)
		copyRawFields(filtered, hostConfig, "CPUShares", "CPUQuota", "CPUPeriod", "NanoCpus", "CpusetCpus")
		result["HostConfig"], _ = json.Marshal(filtered)
	}
	return json.Marshal(result)
}

func copyRawFields(destination map[string]json.RawMessage, source map[string]json.RawMessage, fields ...string) {
	for _, field := range fields {
		if value, ok := source[field]; ok {
			destination[field] = value
		}
	}
}

func object(value json.RawMessage) (map[string]json.RawMessage, bool) {
	if len(value) == 0 || string(value) == "null" {
		return nil, false
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, false
	}
	return result, true
}

func sanitizeLabels(value json.RawMessage) json.RawMessage {
	labels, ok := object(value)
	if !ok {
		return json.RawMessage(`{}`)
	}
	filtered := make(map[string]json.RawMessage)
	for key, item := range labels {
		if _, ok := safeDockerLabels[key]; ok {
			filtered[key] = item
		}
	}
	encoded, err := json.Marshal(filtered)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// ConfigFromEnv is kept here so the binary and tests share the same defaults.
func ConfigFromEnv() Config {
	return Config{
		ListenAddress: firstNonEmpty(os.Getenv("LISTEN_ADDR"), defaultListenAddress),
		SocketPath:    firstNonEmpty(os.Getenv("DOCKER_SOCKET"), defaultSocketPath),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
