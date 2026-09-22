package domainname

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeHostname(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ordinary", input: "example.com", want: "example.com"},
		{name: "lowercases", input: "Example.COM", want: "example.com"},
		{name: "trailing dot", input: "example.com.", want: "example.com"},
		{name: "nested", input: "sub.example.com", want: "sub.example.com"},
		{name: "single label hostname", input: "localhost", want: "localhost"},
		{name: "unicode IDN", input: "例え.テスト", want: "xn--r8jz45g.xn--zckzah"},
		{name: "equivalent punycode", input: "XN--R8JZ45G.XN--ZCKZAH.", want: "xn--r8jz45g.xn--zckzah"},
		{name: "surrounding whitespace", input: "  Example.COM.  ", want: "example.com"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeHostname(test.input)
			if err != nil {
				t.Fatalf("NormalizeHostname(%q) error = %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeHostname(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestNormalizeHostnameRejectsUnsafeValues(t *testing.T) {
	oversizedLabel := strings.Repeat("a", 64) + ".com"
	oversizedHostname := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 63),
		"e",
	}, ".")
	invalidUTF8 := string([]byte{'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 0xff})
	for _, input := range []string{
		"",
		"example..com",
		"example.com..",
		"-example.com",
		"example-.com",
		"xn--.com",
		oversizedLabel,
		oversizedHostname,
		"127.0.0.1",
		"127.0.0.1.",
		"127.000.000.001",
		"[::1]",
		"example.com:443",
		"https://example.com",
		"user@example.com",
		"example.com/path",
		"example.com?query",
		"example.com#fragment",
		"example.com\\path",
		"example\t.com",
		"example\x00.com",
		invalidUTF8,
	} {
		if _, err := NormalizeHostname(input); !errors.Is(err, ErrInvalidHostname) {
			t.Errorf("NormalizeHostname(%q) error = %v, want ErrInvalidHostname", input, err)
		}
	}
}

func TestRegistrableDomain(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "com", input: "app.example.com", want: "example.com"},
		{name: "co uk", input: "app.example.co.uk", want: "example.co.uk"},
		{name: "preserves IDN canonical form", input: "app.例え.テスト", want: "xn--r8jz45g.xn--zckzah"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := RegistrableDomain(test.input)
			if err != nil {
				t.Fatalf("RegistrableDomain(%q) error = %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("RegistrableDomain(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
	for _, input := range []string{"com", "co.uk", "例え"} {
		if _, err := RegistrableDomain(input); !errors.Is(err, ErrInvalidRegistrableDomain) {
			t.Errorf("RegistrableDomain(%q) error = %v, want ErrInvalidRegistrableDomain", input, err)
		}
	}
}

func TestIsSubdomain(t *testing.T) {
	if !IsSubdomain("shop.apps.example.com", "apps.example.com") {
		t.Fatal("IsSubdomain did not recognize a strict subdomain")
	}
	if IsSubdomain("apps.example.com", "apps.example.com") {
		t.Fatal("IsSubdomain treated an equal hostname as a strict subdomain")
	}
	if IsSubdomain("shop.example.com", "ample.com") {
		t.Fatal("IsSubdomain accepted a label-boundary mismatch")
	}
	if IsSubdomain("127.0.0.1", "example.com") {
		t.Fatal("IsSubdomain accepted an IP hostname")
	}
}
