package gitarchive

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseGitHubRepository(t *testing.T) {
	archive, err := Parse("https://github.com/Acme/landing.git", "feature/home")
	if err != nil {
		t.Fatal(err)
	}
	if archive.Provider != ProviderGitHub || archive.Repository != "https://github.com/Acme/landing" || archive.Ref != "feature/home" {
		t.Fatalf("unexpected descriptor: %+v", archive)
	}
	if archive.ArchiveURL != "https://codeload.github.com/Acme/landing/tar.gz/feature/home" {
		t.Fatalf("archive URL = %q", archive.ArchiveURL)
	}
	if archive.Filename != "git-github-landing-feature-home.tar.gz" {
		t.Fatalf("filename = %q", archive.Filename)
	}
}

func TestParseGitLabNestedRepository(t *testing.T) {
	archive, err := Parse("https://gitlab.com/acme/web/landing", "release/2026.09")
	if err != nil {
		t.Fatal(err)
	}
	if archive.Provider != ProviderGitLab || archive.Repository != "https://gitlab.com/acme/web/landing" || archive.Ref != "release/2026.09" {
		t.Fatalf("unexpected descriptor: %+v", archive)
	}
	if archive.ArchiveURL != "https://gitlab.com/acme/web/landing/-/archive/release/2026.09/landing-release-2026.09.tar.gz" {
		t.Fatalf("archive URL = %q", archive.ArchiveURL)
	}
}

func TestParseRejectsUnsafeRepositoryAndRef(t *testing.T) {
	for _, value := range []string{
		"http://github.com/acme/web",
		"https://example.com/acme/web",
		"https://github.com/acme/web/extra",
		"https://github.com/acme/../web",
		"https://github.com/acme/web?token=secret",
	} {
		if _, err := Parse(value, "main"); !errors.Is(err, ErrInvalidRepository) {
			t.Fatalf("Parse(%q) error = %v, want ErrInvalidRepository", value, err)
		}
	}
	for _, value := range []string{"../main", "feature//unsafe", "feature/..", "release.lock", "a?b", "a b", strings.Repeat("a", maxRefBytes+1)} {
		if _, err := Parse("https://github.com/acme/web", value); !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("ref %q error = %v, want ErrInvalidRef", value, err)
		}
	}
	longGroup := strings.Repeat(strings.Repeat("g", 100)+"/", 5) + strings.Repeat("p", 100)
	if _, err := Parse("https://gitlab.com/"+longGroup, "main"); !errors.Is(err, ErrInvalidRepository) {
		t.Fatalf("overlong repository error = %v, want ErrInvalidRepository", err)
	}
}

func TestFetcherRejectsRedirectsAndBoundsBody(t *testing.T) {
	var requested string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requested = request.URL.String()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("0123456789")), Header: make(http.Header), Request: request}, nil
	})}
	fetcher := &Fetcher{Client: client}
	archive, err := fetcher.Fetch(context.Background(), "https://github.com/acme/web", "main", 5)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Body.Close()
	data, err := io.ReadAll(archive.Body)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ReadAll error = %v, want ErrTooLarge", err)
	}
	if string(data) != "01234" {
		t.Fatalf("bounded data = %q", data)
	}
	if requested != "https://codeload.github.com/acme/web/tar.gz/main" {
		t.Fatalf("requested URL = %q", requested)
	}
}

func TestFetcherMapsNonOK(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("not found")), Header: make(http.Header), Request: request}, nil
	})}
	_, err := (&Fetcher{Client: client}).Fetch(context.Background(), "https://github.com/acme/web", "main", 10)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Fetch error = %v, want ErrUnavailable", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
