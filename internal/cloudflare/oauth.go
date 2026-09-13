// OAuth support is retained as an experimental provider adapter only. Browser
// setup intentionally does not invoke it until a verified Cloudflare OAuth
// client, redirect URI, and scope set are available for the deployed origin.
package cloudflare

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
	defaultOAuthAuthorizationURL = "https://dash.cloudflare.com/oauth2/auth"
	defaultOAuthTokenURL         = "https://dash.cloudflare.com/oauth2/token"
)

type OAuthClient struct {
	ClientID      string
	ClientSecret  string
	Authorization string
	TokenURL      string
	HTTPClient    *http.Client
}

type OAuthToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

func NewOAuthClient(clientID, clientSecret string, httpClient *http.Client) (*OAuthClient, error) {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	if clientID == "" || clientSecret == "" || strings.ContainsAny(clientID+clientSecret, "\x00\r\n") {
		return nil, errors.New("Cloudflare OAuth is not configured")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &OAuthClient{ClientID: clientID, ClientSecret: clientSecret, Authorization: defaultOAuthAuthorizationURL, TokenURL: defaultOAuthTokenURL, HTTPClient: httpClient}, nil
}

func (c *OAuthClient) AuthorizationURL(redirectURL, state string, scopes []string) (string, error) {
	if c == nil || c.ClientID == "" {
		return "", errors.New("Cloudflare OAuth is not configured")
	}
	if !validRedirectURL(redirectURL) || strings.TrimSpace(state) == "" || strings.ContainsAny(state, "\x00\r\n") {
		return "", errors.New("Cloudflare OAuth redirect or state is invalid")
	}
	values := url.Values{}
	values.Set("response_type", "code")
	values.Set("client_id", c.ClientID)
	values.Set("redirect_uri", redirectURL)
	values.Set("state", state)
	if len(scopes) > 0 {
		values.Set("scope", strings.Join(scopes, " "))
	}
	return c.Authorization + "?" + values.Encode(), nil
}

func (c *OAuthClient) Exchange(ctx context.Context, code, redirectURL string) (OAuthToken, error) {
	if c == nil || c.HTTPClient == nil || c.ClientID == "" || c.ClientSecret == "" {
		return OAuthToken{}, errors.New("Cloudflare OAuth is not configured")
	}
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 4096 || strings.ContainsAny(code, "\x00\r\n") || !validRedirectURL(redirectURL) {
		return OAuthToken{}, errors.New("Cloudflare OAuth callback is invalid")
	}
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURL)
	form.Set("client_id", c.ClientID)
	form.Set("client_secret", c.ClientSecret)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("create Cloudflare OAuth token request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		return OAuthToken{}, fmt.Errorf("Cloudflare OAuth token request failed: %w", err)
	}
	defer response.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(response.Body, 256<<10))
	if err != nil {
		return OAuthToken{}, fmt.Errorf("read Cloudflare OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return OAuthToken{}, fmt.Errorf("Cloudflare OAuth token request was rejected (HTTP %d)", response.StatusCode)
	}
	var token OAuthToken
	if err := json.Unmarshal(contents, &token); err != nil || strings.TrimSpace(token.AccessToken) == "" {
		return OAuthToken{}, errors.New("Cloudflare OAuth returned an invalid token")
	}
	return token, nil
}

func validRedirectURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}
