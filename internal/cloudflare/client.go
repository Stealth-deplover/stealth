// Package cloudflare contains the narrow Cloudflare control-plane adapter used
// by browser setup. It deliberately exposes only account, zone, tunnel, DNS,
// and status operations needed by Stealth.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultAPIBaseURL = "https://api.cloudflare.com/client/v4"

type Account struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_on,omitempty"`
}

type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type Tunnel struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status,omitempty"`
	Token     string `json:"token,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
}

type DNSRecord struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

type IngressRule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
	Origin   string `json:"originRequest,omitempty"`
}

type TunnelStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Status      string `json:"status"`
	Connections int    `json:"connections,omitempty"`
}

// UnmarshalJSON accepts both the legacy integer shape used by older mocks and
// Cloudflare's current tunnel response, where connections is an array that is
// being phased out in favor of the dedicated connections endpoint.
func (s *TunnelStatus) UnmarshalJSON(contents []byte) error {
	var raw struct {
		ID          string          `json:"id"`
		Name        string          `json:"name"`
		Status      string          `json:"status"`
		Connections json.RawMessage `json:"connections"`
	}
	if err := json.Unmarshal(contents, &raw); err != nil {
		return err
	}
	s.ID, s.Name, s.Status, s.Connections = raw.ID, raw.Name, raw.Status, 0
	if len(raw.Connections) == 0 || string(raw.Connections) == "null" {
		return nil
	}
	var count int
	if err := json.Unmarshal(raw.Connections, &count); err == nil {
		s.Connections = count
		return nil
	}
	var connections []json.RawMessage
	if err := json.Unmarshal(raw.Connections, &connections); err != nil {
		return err
	}
	s.Connections = len(connections)
	return nil
}

type Client interface {
	ListAccounts(context.Context) ([]Account, error)
	ListZones(context.Context, string) ([]Zone, error)
	CreateTunnel(context.Context, string, string) (Tunnel, error)
	ConfigureTunnel(context.Context, string, string, []IngressRule) error
	CreateDNSRecord(context.Context, string, DNSRecord) (DNSRecord, error)
	TunnelStatus(context.Context, string, string) (TunnelStatus, error)
	TunnelToken(context.Context, string, string) (string, error)
}

type APIClient struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
}

func NewClient(apiToken, baseURL string, httpClient *http.Client) (*APIClient, error) {
	apiToken = strings.TrimSpace(apiToken)
	if apiToken == "" {
		return nil, errors.New("Cloudflare API token is required")
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultAPIBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Cloudflare API base URL is invalid")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &APIClient{baseURL: baseURL, apiToken: apiToken, httpClient: httpClient}, nil
}

func (c *APIClient) ListAccounts(ctx context.Context) ([]Account, error) {
	var result struct {
		Result []Account `json:"result"`
	}
	if err := c.do(ctx, http.MethodGet, "/accounts?per_page=100", nil, &result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

func (c *APIClient) ListZones(ctx context.Context, accountID string) ([]Zone, error) {
	accountID, err := safeID(accountID, "account")
	if err != nil {
		return nil, err
	}
	var result struct {
		Result []Zone `json:"result"`
	}
	path := "/zones?per_page=100&account.id=" + url.QueryEscape(accountID)
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

func (c *APIClient) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	accountID, err := safeID(accountID, "account")
	if err != nil {
		return Tunnel{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 || strings.ContainsAny(name, "\x00\r\n") {
		return Tunnel{}, errors.New("tunnel name is invalid")
	}
	var result struct {
		Result Tunnel `json:"result"`
	}
	body := map[string]string{"name": name, "config_src": "cloudflare"}
	if err := c.do(ctx, http.MethodPost, "/accounts/"+accountID+"/cfd_tunnel", body, &result); err != nil {
		return Tunnel{}, err
	}
	if strings.TrimSpace(result.Result.ID) == "" {
		return Tunnel{}, errors.New("Cloudflare returned an invalid tunnel")
	}
	return result.Result, nil
}

func (c *APIClient) ConfigureTunnel(ctx context.Context, accountID, tunnelID string, ingress []IngressRule) error {
	accountID, err := safeID(accountID, "account")
	if err != nil {
		return err
	}
	tunnelID, err = safeID(tunnelID, "tunnel")
	if err != nil {
		return err
	}
	if len(ingress) == 0 || len(ingress) > 64 {
		return errors.New("tunnel ingress configuration is invalid")
	}
	for _, rule := range ingress {
		if strings.TrimSpace(rule.Service) == "" || len(rule.Service) > 2048 || strings.ContainsAny(rule.Service, "\x00\r\n") {
			return errors.New("tunnel ingress service is invalid")
		}
	}
	body := map[string]any{"config": map[string]any{"ingress": ingress}}
	return c.do(ctx, http.MethodPut, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/configurations", body, &struct{}{})
}

func (c *APIClient) CreateDNSRecord(ctx context.Context, zoneID string, record DNSRecord) (DNSRecord, error) {
	zoneID, err := safeID(zoneID, "zone")
	if err != nil {
		return DNSRecord{}, err
	}
	record.Type = strings.ToUpper(strings.TrimSpace(record.Type))
	record.Name = strings.TrimSpace(record.Name)
	record.Content = strings.TrimSpace(record.Content)
	if record.Type != "CNAME" || record.Name == "" || record.Content == "" || len(record.Name) > 253 || len(record.Content) > 253 || strings.ContainsAny(record.Name+record.Content, "\x00\r\n") {
		return DNSRecord{}, errors.New("DNS record is invalid")
	}
	if record.TTL == 0 {
		record.TTL = 1
	}
	var result struct {
		Result DNSRecord `json:"result"`
	}
	if err := c.do(ctx, http.MethodPost, "/zones/"+zoneID+"/dns_records", record, &result); err != nil {
		return DNSRecord{}, err
	}
	if strings.TrimSpace(result.Result.ID) == "" {
		return DNSRecord{}, errors.New("Cloudflare returned an invalid DNS record")
	}
	return result.Result, nil
}

func (c *APIClient) TunnelStatus(ctx context.Context, accountID, tunnelID string) (TunnelStatus, error) {
	accountID, err := safeID(accountID, "account")
	if err != nil {
		return TunnelStatus{}, err
	}
	tunnelID, err = safeID(tunnelID, "tunnel")
	if err != nil {
		return TunnelStatus{}, err
	}
	var result struct {
		Result TunnelStatus `json:"result"`
	}
	if err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID, nil, &result); err != nil {
		return TunnelStatus{}, err
	}
	return result.Result, nil
}

func (c *APIClient) TunnelToken(ctx context.Context, accountID, tunnelID string) (string, error) {
	accountID, err := safeID(accountID, "account")
	if err != nil {
		return "", err
	}
	tunnelID, err = safeID(tunnelID, "tunnel")
	if err != nil {
		return "", err
	}
	var result struct {
		Result string `json:"result"`
	}
	if err := c.do(ctx, http.MethodGet, "/accounts/"+accountID+"/cfd_tunnel/"+tunnelID+"/token", nil, &result); err != nil {
		return "", err
	}
	if strings.TrimSpace(result.Result) == "" {
		return "", errors.New("Cloudflare returned an empty tunnel token")
	}
	return result.Result, nil
}

type apiError struct {
	Code    json.Number `json:"code"`
	Message string      `json:"message"`
}

type apiEnvelope struct {
	Success bool       `json:"success"`
	Errors  []apiError `json:"errors"`
}

func (c *APIClient) do(ctx context.Context, method, path string, body any, result any) error {
	if c == nil || c.httpClient == nil {
		return errors.New("Cloudflare client is not configured")
	}
	var requestBody io.Reader
	if body != nil {
		contents, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode Cloudflare request: %w", err)
		}
		requestBody = bytes.NewReader(contents)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, requestBody)
	if err != nil {
		return fmt.Errorf("create Cloudflare request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Cloudflare request failed: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read Cloudflare response: %w", err)
	}
	var envelope apiEnvelope
	if err := json.Unmarshal(contents, &envelope); err != nil {
		return fmt.Errorf("Cloudflare returned invalid JSON (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		return fmt.Errorf("Cloudflare request was rejected (HTTP %d): %s", response.StatusCode, publicAPIError(envelope.Errors))
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(contents, result); err != nil {
		return fmt.Errorf("decode Cloudflare response: %w", err)
	}
	return nil
}

func safeID(value, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "\x00\r\n/\\?#[ ]") {
		return "", fmt.Errorf("%s ID is invalid", label)
	}
	return value, nil
}

func publicAPIError(errors []apiError) string {
	if len(errors) == 0 {
		return "the provider did not return an explanation"
	}
	message := strings.TrimSpace(errors[0].Message)
	if message == "" {
		message = "the provider did not return an explanation"
	}
	if len(message) > 180 {
		message = message[:180]
	}
	if errors[0].Code != "" {
		return "error " + errors[0].Code.String() + ": " + message
	}
	return message
}

func StatusIsHealthy(status TunnelStatus) bool {
	return strings.EqualFold(strings.TrimSpace(status.Status), "healthy")
}

func NumericStatus(value int) string { return strconv.Itoa(value) }
