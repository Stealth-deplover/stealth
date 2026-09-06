package repository

import (
	"errors"
	"testing"
)

func TestNormalizeCORSOrigin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "https host", raw: "https://APP.Example.com", want: "https://app.example.com", ok: true},
		{name: "default https port", raw: "https://app.example.com:443", want: "https://app.example.com", ok: true},
		{name: "localhost port", raw: "http://localhost:3000", want: "http://localhost:3000", ok: true},
		{name: "path rejected", raw: "https://app.example.com/callback", ok: false},
		{name: "wildcard rejected", raw: "https://*.example.com", ok: false},
		{name: "credentials rejected", raw: "https://user:pass@app.example.com", ok: false},
		{name: "query rejected", raw: "https://app.example.com?x=1", ok: false},
		{name: "scheme rejected", raw: "ftp://app.example.com", ok: false},
		{name: "port rejected", raw: "https://app.example.com:65536", ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeCORSOrigin(test.raw)
			if test.ok {
				if err != nil || got != test.want {
					t.Fatalf("NormalizeCORSOrigin(%q) = %q, %v; want %q", test.raw, got, err, test.want)
				}
				return
			}
			if !errors.Is(err, ErrInvalidCORSOrigin) {
				t.Fatalf("NormalizeCORSOrigin(%q) error = %v; want ErrInvalidCORSOrigin", test.raw, err)
			}
		})
	}
}

func TestNormalizeCORSOriginsCanonicalizesAndRejectsDuplicates(t *testing.T) {
	got, err := NormalizeCORSOrigins([]string{"https://z.example", "http://localhost:3000"})
	if err != nil {
		t.Fatalf("NormalizeCORSOrigins() error = %v", err)
	}
	if len(got) != 2 || got[0] != "http://localhost:3000" || got[1] != "https://z.example" {
		t.Fatalf("NormalizeCORSOrigins() = %#v", got)
	}
	if _, err := NormalizeCORSOrigins([]string{"https://app.example", "HTTPS://APP.EXAMPLE"}); !errors.Is(err, ErrInvalidCORSOrigin) {
		t.Fatalf("duplicate origins error = %v; want ErrInvalidCORSOrigin", err)
	}
}
