package githubauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientDeviceFlowAndUserLookup(t *testing.T) {
	var deviceRequest bool
	var pollRequest bool
	var userRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/device":
			deviceRequest = true
			if request.Method != http.MethodPost || request.URL.Query().Get("client_id") != "Iv1.test-client" {
				t.Errorf("device request = %s %s", request.Method, request.URL.String())
			}
			if request.Header.Get("Accept") != "application/vnd.github+json" || request.Header.Get("X-GitHub-Api-Version") != apiVersion {
				t.Errorf("device headers = %#v", request.Header)
			}
			_, _ = io.WriteString(writer, `{"device_code":"device-secret","user_code":"WDJB-MJHT","verification_uri":"https://github.com/login/device","expires_in":900,"interval":5}`)
		case "/token":
			pollRequest = true
			body, _ := io.ReadAll(request.Body)
			form, err := url.ParseQuery(string(body))
			if err != nil || form.Get("client_id") != "Iv1.test-client" || form.Get("device_code") != "device-secret" || form.Get("grant_type") != deviceGrant {
				t.Errorf("token form = %q", body)
			}
			_, _ = io.WriteString(writer, `{"access_token":"server-only-token","token_type":"bearer"}`)
		case "/user":
			userRequest = true
			if request.Header.Get("Authorization") != "Bearer server-only-token" {
				t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
			}
			_, _ = io.WriteString(writer, `{"id":424242,"login":"stealth-owner","email":null,"avatar_url":"https://avatars.githubusercontent.com/u/424242","name":"Stealth Owner"}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.Client())
	client.DeviceURL = server.URL + "/device"
	client.TokenURL = server.URL + "/token"
	client.UserURL = server.URL + "/user"

	device, err := client.RequestDeviceCode(context.Background(), "Iv1.test-client")
	if err != nil {
		t.Fatal(err)
	}
	if device.DeviceCode != "device-secret" || device.UserCode != "WDJB-MJHT" || device.ExpiresIn != 15*time.Minute || device.PollingInterval != 5*time.Second {
		t.Fatalf("device authorization = %#v", device)
	}
	result, err := client.PollAccessToken(context.Background(), "Iv1.test-client", device.DeviceCode)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != PollAuthorized || result.AccessToken != "server-only-token" {
		t.Fatalf("poll result = %#v", result)
	}
	user, err := client.GetUser(context.Background(), result.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if user.ID != 424242 || user.Login != "stealth-owner" || user.Email != "" {
		t.Fatalf("GitHub user = %#v", user)
	}
	if !deviceRequest || !pollRequest || !userRequest {
		t.Fatalf("requests device=%v poll=%v user=%v", deviceRequest, pollRequest, userRequest)
	}
}

func TestHTTPClientPollStatuses(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   string
		status PollStatus
	}{
		{name: "pending", body: `{"error":"authorization_pending"}`, status: PollPending},
		{name: "slow down", body: `{"error":"slow_down"}`, status: PollSlowDown},
		{name: "expired", body: `{"error":"expired_token"}`, status: PollExpired},
		{name: "alternate expired", body: `{"error":"token_expired"}`, status: PollExpired},
		{name: "denied", body: `{"error":"access_denied"}`, status: PollDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)
			client := NewClient(server.Client())
			client.TokenURL = server.URL
			result, err := client.PollAccessToken(context.Background(), "client", "device")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.status || result.AccessToken != "" {
				t.Fatalf("poll result = %#v, want %q without token", result, test.status)
			}
		})
	}
}

func TestHTTPClientPollStatusesFromProviderHTTPErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		code   string
		status PollStatus
	}{
		{name: "pending", code: "authorization_pending", status: PollPending},
		{name: "slow down", code: "slow_down", status: PollSlowDown},
		{name: "expired", code: "expired_token", status: PollExpired},
		{name: "denied", code: "access_denied", status: PollDenied},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(writer, `{"error":"`+test.code+`"}`)
			}))
			t.Cleanup(server.Close)
			client := NewClient(server.Client())
			client.TokenURL = server.URL
			result, err := client.PollAccessToken(context.Background(), "client", "device")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.status || result.AccessToken != "" {
				t.Fatalf("poll result = %#v, want %q without token", result, test.status)
			}
		})
	}
}

func TestHTTPClientRejectsUnexpectedDeviceResponse(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{name: "missing device code", body: `{"user_code":"WDJB-MJHT","verification_uri":"https://github.com/login/device","expires_in":900}`},
		{name: "unexpected verification URL", body: `{"device_code":"secret","user_code":"WDJB-MJHT","verification_uri":"https://evil.example/setup","expires_in":900}`},
		{name: "provider error", body: `{"error":"invalid_client","error_description":"bad client"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, test.body)
			}))
			t.Cleanup(server.Close)
			client := NewClient(server.Client())
			client.DeviceURL = server.URL
			if _, err := client.RequestDeviceCode(context.Background(), "client"); err == nil {
				t.Fatal("unexpected device response was accepted")
			}
		})
	}
}

func TestHTTPClientDoesNotIncludeProviderSecretsInErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(writer, `{"error":"server_error","error_description":"device-secret"}`)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.TokenURL = server.URL
	_, err := client.PollAccessToken(context.Background(), "client", "device-secret")
	if err == nil || strings.Contains(err.Error(), "device-secret") {
		t.Fatalf("provider error = %v; device code must not be echoed", err)
	}
}

func TestHTTPClientGetUserValidatesIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"id": 0, "login": ""})
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.UserURL = server.URL
	if _, err := client.GetUser(context.Background(), "token"); err == nil {
		t.Fatal("incomplete GitHub identity was accepted")
	}
}

func TestAuthorizationURLUsesHTTPSCallbackAndPKCE(t *testing.T) {
	verifier, challenge, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) != 43 || len(challenge) != 43 || strings.ContainsAny(verifier+challenge, "+/=\r\n") {
		t.Fatalf("PKCE values are not URL-safe: verifier=%q challenge=%q", verifier, challenge)
	}
	location, err := AuthorizationURL("Iv1.test-client", "https://setup.example.test/v1/setup/github/authorize/callback", "callback-state", challenge)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if parsed.Host != "github.com" || parsed.Path != "/login/oauth/authorize" || query.Get("client_id") != "Iv1.test-client" || query.Get("redirect_uri") != "https://setup.example.test/v1/setup/github/authorize/callback" || query.Get("state") != "callback-state" || query.Get("code_challenge") != challenge || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorization URL = %q", location)
	}
	if _, err := AuthorizationURL("Iv1.test-client", "http://setup.example.test/callback", "state", challenge); err == nil {
		t.Fatal("AuthorizationURL accepted an HTTP callback")
	}
}

func TestHTTPClientExchangesBrowserAuthorizationCodeWithoutReturningProviderSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/token" || request.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Fatalf("OAuth exchange request = %s %s headers=%#v", request.Method, request.URL, request.Header)
		}
		body, _ := io.ReadAll(request.Body)
		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("client_id") != "Iv1.test-client" || form.Get("client_secret") != "client-secret" || form.Get("code") != "one-time-code" || form.Get("redirect_uri") != "https://setup.example.test/callback" || form.Get("code_verifier") != "verifier-abcdefghijklmnopqrstuvwxyz-0123456789" {
			t.Fatalf("OAuth exchange form = %q", body)
		}
		_, _ = io.WriteString(writer, `{"access_token":"server-only-token","token_type":"bearer"}`)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.TokenURL = server.URL + "/token"
	result, err := client.ExchangeAuthorizationCode(context.Background(), "Iv1.test-client", "client-secret", "one-time-code", "https://setup.example.test/callback", "verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	if err != nil || result.AccessToken != "server-only-token" {
		t.Fatalf("OAuth exchange = %#v, %v", result, err)
	}
}

func TestHTTPClientBrowserAuthorizationProviderFailureIsGeneric(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, `{"error":"bad_verification_code","error_description":"one-time-code"}`)
	}))
	defer server.Close()
	client := NewClient(server.Client())
	client.TokenURL = server.URL
	_, err := client.ExchangeAuthorizationCode(context.Background(), "Iv1.test-client", "client-secret", "one-time-code", "https://setup.example.test/callback", "verifier-abcdefghijklmnopqrstuvwxyz-0123456789")
	if err == nil || strings.Contains(err.Error(), "one-time-code") || strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("OAuth provider error = %v", err)
	}
}
