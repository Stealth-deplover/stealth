package messagingrunner

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestRegistryNormalizesFixedAdapterIdentity(t *testing.T) {
	registry := NewRegistry()
	adapter := AdapterFunc(func(context.Context, Provider, Message) error { return nil })
	if err := registry.Register(" EMAIL ", " LOG ", adapter); err != nil {
		t.Fatal(err)
	}
	if registry.Resolve("email", "log") == nil || registry.Resolve(" EMAIL ", " LOG ") == nil {
		t.Fatal("normalized adapter was not resolved")
	}
	if err := registry.Register("email\n", "log", adapter); err == nil {
		t.Fatal("control character in adapter identity was accepted")
	}
	if err := registry.Register("email", "log", nil); err == nil {
		t.Fatal("nil adapter was accepted")
	}
}

func TestTwilioAdapterUsesFixedEndpointAndClassifiesResponses(t *testing.T) {
	provider := Provider{Channel: "sms", Name: "twilio", Enabled: true, Credentials: map[string]string{
		"account_sid": "test-account-sid",
		"auth_token":  "token-123456",
		"from":        "+15551234567",
	}}
	message := Message{Channel: "sms", Recipient: "+15557654321", Body: "hello & goodbye"}
	var seen *http.Request
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		seen = request
		return &http.Response{StatusCode: http.StatusCreated, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"sid":"SM123"}`)), Request: request}, nil
	})}
	if err := (TwilioAdapter{Client: client}).Send(context.Background(), provider, message); err != nil {
		t.Fatal(err)
	}
	if seen == nil || seen.Method != http.MethodPost || seen.URL.String() != "https://api.twilio.com/2010-04-01/Accounts/test-account-sid/Messages.json" {
		t.Fatalf("unexpected Twilio request: %+v", seen)
	}
	if seen.URL.Host != "api.twilio.com" || seen.URL.Scheme != "https" {
		t.Fatalf("Twilio endpoint was not fixed HTTPS: %s", seen.URL)
	}
	account, token, ok := seen.BasicAuth()
	if !ok || account != provider.Credentials["account_sid"] || token != provider.Credentials["auth_token"] {
		t.Fatal("Twilio credentials were not sent as basic auth")
	}
	encodedBody, err := io.ReadAll(seen.Body)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(encodedBody))
	if err != nil {
		t.Fatal(err)
	}
	if values.Get("To") != message.Recipient || values.Get("Body") != message.Body || values.Get("From") != provider.Credentials["from"] {
		t.Fatalf("unexpected Twilio form: %v", values)
	}

	for _, test := range []struct {
		name      string
		status    int
		retryable bool
	}{
		{name: "rate limited", status: http.StatusTooManyRequests, retryable: true},
		{name: "server failure", status: http.StatusBadGateway, retryable: true},
		{name: "invalid request", status: http.StatusBadRequest, retryable: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := TwilioAdapter{Client: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("provider failure")), Request: request}, nil
			})}}
			err := adapter.Send(context.Background(), provider, message)
			var sendErr *SendError
			if err == nil || !strings.Contains(err.Error(), "twilio returned") {
				t.Fatalf("unexpected nil/error: %v", err)
			}
			if !errors.As(err, &sendErr) || sendErr.Retryable != test.retryable || sendErr.StatusCode != test.status {
				t.Fatalf("unexpected classified error: %#v", err)
			}
		})
	}
}

func TestProviderAdaptersFailClosedBeforeNetwork(t *testing.T) {
	if err := (SMTPAdapter{}).Send(context.Background(), Provider{Channel: "email", Credentials: map[string]string{"host": "smtp.example.com"}}, Message{Channel: "email", Recipient: "person@example.com", Subject: "subject", Body: "body"}); err == nil || !strings.Contains(err.Error(), "requires host and from") {
		t.Fatalf("incomplete SMTP credentials were not rejected: %v", err)
	}
	if err := (TwilioAdapter{Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid credentials reached network")
		return nil, nil
	})}}).Send(context.Background(), Provider{Channel: "sms", Credentials: map[string]string{"account_sid": "not-valid", "auth_token": "secret"}}, Message{Channel: "sms", Recipient: "+15551234567", Body: "body"}); err == nil {
		t.Fatal("invalid Twilio credentials were accepted")
	}
	if _, err := resolvePublicIPs(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("loopback SMTP destination was accepted")
	}
	if _, err := resolvePublicIPs(context.Background(), "::1"); err == nil {
		t.Fatal("IPv6 loopback SMTP destination was accepted")
	}
}

func TestRetryAndErrorBounds(t *testing.T) {
	if !retryableStatus(http.StatusRequestTimeout) || !retryableStatus(http.StatusTooEarly) || !retryableStatus(http.StatusTooManyRequests) || !retryableStatus(http.StatusInternalServerError) {
		t.Fatal("transient status was not retryable")
	}
	if retryableStatus(http.StatusBadRequest) || retryableStatus(http.StatusUnauthorized) || retryableStatus(http.StatusNotFound) {
		t.Fatal("permanent status was marked retryable")
	}
	if got := len(safeError(errors.New(strings.Repeat("x", 5000)))); got != 1000 {
		t.Fatalf("safeError length = %d, want 1000", got)
	}
}
