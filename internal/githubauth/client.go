// Package githubauth contains the narrow GitHub App Device Flow client used
// by first-owner bootstrap. It intentionally exposes no GitHub token to the
// HTTP or Console layers.
package githubauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	deviceCodeURL = "https://github.com/login/device/code"
	tokenURL      = "https://github.com/login/oauth/access_token"
	userURL       = "https://api.github.com/user"
	apiVersion    = "2022-11-28"
	deviceGrant   = "urn:ietf:params:oauth:grant-type:device_code"
	maxResponse   = 64 << 10
)

// DeviceVerificationURI is a fixed GitHub URL. A temporary TryCloudflare
// hostname is never used as an OAuth callback.
const DeviceVerificationURI = "https://github.com/login/device"

type DeviceAuthorization struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	PollingInterval time.Duration
}

type PollStatus string

const (
	PollAuthorized PollStatus = "authorized"
	PollPending    PollStatus = "authorization_pending"
	PollSlowDown   PollStatus = "slow_down"
	PollExpired    PollStatus = "expired_token"
	PollDenied     PollStatus = "access_denied"
)

type PollResult struct {
	Status      PollStatus
	AccessToken string
}

type User struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
	Name      string `json:"name"`
}

// Client is deliberately small so all provider behavior can be tested with
// an httptest server or a deterministic fake. Implementations must not log or
// return access tokens beyond the server-side polling boundary.
type Client interface {
	RequestDeviceCode(ctx context.Context, clientID string) (DeviceAuthorization, error)
	PollAccessToken(ctx context.Context, clientID, deviceCode string) (PollResult, error)
	GetUser(ctx context.Context, accessToken string) (User, error)
}

type HTTPClient struct {
	HTTPClient *http.Client
	DeviceURL  string
	TokenURL   string
	UserURL    string
}

func NewClient(client *http.Client) *HTTPClient {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &HTTPClient{
		HTTPClient: client,
		DeviceURL:  deviceCodeURL,
		TokenURL:   tokenURL,
		UserURL:    userURL,
	}
}

type providerError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e providerError) Error() string {
	if e.Code == "" {
		return "GitHub returned an invalid authorization response"
	}
	return "GitHub authorization failed: " + e.Code
}

func (c *HTTPClient) RequestDeviceCode(ctx context.Context, clientID string) (DeviceAuthorization, error) {
	if strings.TrimSpace(clientID) == "" {
		return DeviceAuthorization{}, errors.New("GitHub App client ID is not configured")
	}
	endpoint, err := url.Parse(c.DeviceURL)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("parse GitHub device endpoint: %w", err)
	}
	query := endpoint.Query()
	query.Set("client_id", clientID)
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), http.NoBody)
	if err != nil {
		return DeviceAuthorization{}, fmt.Errorf("create GitHub device request: %w", err)
	}
	setGitHubHeaders(request)
	var response struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		providerError
	}
	if err := c.doJSON(request, &response); err != nil {
		return DeviceAuthorization{}, err
	}
	if response.Code != "" {
		return DeviceAuthorization{}, response.providerError
	}
	response.DeviceCode = strings.TrimSpace(response.DeviceCode)
	response.UserCode = strings.TrimSpace(response.UserCode)
	if response.DeviceCode == "" || len(response.DeviceCode) > 2048 || response.UserCode == "" || response.ExpiresIn <= 0 {
		return DeviceAuthorization{}, errors.New("GitHub returned an incomplete device authorization")
	}
	interval := response.Interval
	if interval < 1 {
		interval = 5
	}
	if response.VerificationURI != DeviceVerificationURI {
		return DeviceAuthorization{}, errors.New("GitHub returned an unexpected device verification URL")
	}
	return DeviceAuthorization{
		DeviceCode:      response.DeviceCode,
		UserCode:        response.UserCode,
		VerificationURI: response.VerificationURI,
		ExpiresIn:       time.Duration(response.ExpiresIn) * time.Second,
		PollingInterval: time.Duration(interval) * time.Second,
	}, nil
}

func (c *HTTPClient) PollAccessToken(ctx context.Context, clientID, deviceCode string) (PollResult, error) {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(deviceCode) == "" {
		return PollResult{}, errors.New("GitHub device authorization is incomplete")
	}
	form := url.Values{
		"client_id":   {clientID},
		"device_code": {deviceCode},
		"grant_type":  {deviceGrant},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return PollResult{}, fmt.Errorf("create GitHub token request: %w", err)
	}
	setGitHubHeaders(request)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		providerError
	}
	if err := c.doJSON(request, &response); err != nil {
		if status, ok := pollStatusFromError(err); ok {
			return PollResult{Status: status}, nil
		}
		return PollResult{}, err
	}
	return pollResult(response.Code, response.AccessToken)
}

func pollResult(code, accessToken string) (PollResult, error) {
	if accessToken != "" {
		return PollResult{Status: PollAuthorized, AccessToken: accessToken}, nil
	}
	switch code {
	case string(PollPending):
		return PollResult{Status: PollPending}, nil
	case string(PollSlowDown):
		return PollResult{Status: PollSlowDown}, nil
	case "expired_token", "token_expired":
		return PollResult{Status: PollExpired}, nil
	case string(PollDenied):
		return PollResult{Status: PollDenied}, nil
	default:
		if code == "" {
			return PollResult{}, errors.New("GitHub returned an incomplete token response")
		}
		return PollResult{}, providerError{Code: code}
	}
}

func pollStatusFromError(err error) (PollStatus, bool) {
	var provider providerError
	if !errors.As(err, &provider) {
		return "", false
	}
	switch provider.Code {
	case string(PollPending):
		return PollPending, true
	case string(PollSlowDown):
		return PollSlowDown, true
	case string(PollDenied):
		return PollDenied, true
	case "expired_token", "token_expired":
		return PollExpired, true
	default:
		return "", false
	}
}

func (c *HTTPClient) GetUser(ctx context.Context, accessToken string) (User, error) {
	if strings.TrimSpace(accessToken) == "" {
		return User{}, errors.New("GitHub access token is empty")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.UserURL, nil)
	if err != nil {
		return User{}, fmt.Errorf("create GitHub user request: %w", err)
	}
	setGitHubHeaders(request)
	request.Header.Set("Authorization", "Bearer "+accessToken)
	var user User
	if err := c.doJSON(request, &user); err != nil {
		return User{}, err
	}
	user.Login = strings.TrimSpace(user.Login)
	if user.ID <= 0 || user.Login == "" {
		return User{}, errors.New("GitHub returned an incomplete user identity")
	}
	return user, nil
}

func setGitHubHeaders(request *http.Request) {
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", apiVersion)
	request.Header.Set("User-Agent", "stealth-instance-bootstrap")
}

func (c *HTTPClient) doJSON(request *http.Request, target any) error {
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var provider providerError
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponse))
		if readErr != nil || json.Unmarshal(body, &provider) != nil {
			return fmt.Errorf("GitHub request returned HTTP %d", response.StatusCode)
		}
		return provider
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponse))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}
