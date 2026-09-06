package httpapi

import (
	"net/http/httptest"
	"testing"
)

func TestRequestHostname(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{host: "WWW.Example.COM:443", want: "www.example.com"},
		{host: "preview.example.test.", want: "preview.example.test"},
		{host: "localhost:8080", want: ""},
		{host: "127.0.0.1:8080", want: ""},
		{host: "[::1]:8080", want: ""},
		{host: "example.com/path", want: ""},
	}
	for _, test := range tests {
		t.Run(test.host, func(t *testing.T) {
			req := httptest.NewRequest("GET", "http://example.invalid/", nil)
			req.Host = test.host
			if got := requestHostname(req); got != test.want {
				t.Fatalf("requestHostname(%q) = %q, want %q", test.host, got, test.want)
			}
		})
	}
}
