package repository

import (
	"errors"
	"testing"
)

func TestNormalizeSiteHostname(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "lowercases and strips root dot", input: "WWW.Example.COM.", want: "www.example.com"},
		{name: "accepts nested labels", input: "preview.eu.example.test", want: "preview.eu.example.test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeSiteHostname(test.input)
			if err != nil {
				t.Fatalf("NormalizeSiteHostname(%q) error = %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeSiteHostname(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeSiteHostnameRejectsUnsafeValues(t *testing.T) {
	for _, input := range []string{
		"",
		"localhost",
		"127.0.0.1",
		"https://example.com",
		"example.com:443",
		"example..com",
		"-example.com",
		"example-.com",
		"example.com/asset",
		"example.com\\asset",
		"*.example.com",
		"例え.テスト",
	} {
		if _, err := NormalizeSiteHostname(input); !errors.Is(err, ErrInvalidSiteDomain) {
			t.Errorf("NormalizeSiteHostname(%q) error = %v, want ErrInvalidSiteDomain", input, err)
		}
	}
}

func TestContainsSiteDomainToken(t *testing.T) {
	if !containsSiteDomainToken([]string{"other", "challenge-token"}, "challenge-token") {
		t.Fatal("containsSiteDomainToken did not find an exact TXT value")
	}
	if containsSiteDomainToken([]string{"challenge-token-extra"}, "challenge-token") {
		t.Fatal("containsSiteDomainToken accepted a partial TXT value")
	}
}
