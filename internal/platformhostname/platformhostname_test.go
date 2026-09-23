package platformhostname

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCandidatesPreferNameAndUseStableUUIDSuffix(t *testing.T) {
	siteID := uuid.MustParse("018f0d5e-7c19-7abc-8d1e-1234567890ab")
	candidates := Candidates("portfolio", siteID)
	if len(candidates) < 2 || candidates[0] != "portfolio" {
		t.Fatalf("Candidates() = %#v, want portfolio first", candidates)
	}
	wantSuffix := strings.ReplaceAll(siteID.String(), "-", "")
	if candidates[1] != "portfolio-"+wantSuffix {
		t.Fatalf("collision candidate = %q, want UUID-derived candidate", candidates[1])
	}
	if candidates[1] != Candidates("portfolio", siteID)[1] {
		t.Fatal("collision candidate is not deterministic")
	}
	suffix := strings.ReplaceAll(siteID.String(), "-", "")
	want := []string{
		"portfolio",
		"portfolio-" + suffix,
		"site-" + suffix,
		suffix,
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("Site allocation sequence = %#v, want compatibility sequence %#v", candidates, want)
	}
}

func TestAppCandidatesUseAppSpecificDeterministicFallbacks(t *testing.T) {
	appID := uuid.MustParse("018f0d5e-7c19-7abc-8d1e-1234567890ab")
	want := []string{
		"backend",
		"backend-018f0d5e7c197abc8d1e1234567890ab",
		"app-018f0d5e7c197abc8d1e1234567890ab",
		"018f0d5e7c197abc8d1e1234567890ab",
	}
	got := AppCandidates("backend", appID)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AppCandidates() = %#v, want %#v", got, want)
	}
	reserved := AppCandidates("api", appID)
	if len(reserved) != 3 || reserved[0] != "api-018f0d5e7c197abc8d1e1234567890ab" || reserved[1] != want[2] || reserved[2] != want[3] {
		t.Fatalf("reserved App candidates = %#v", reserved)
	}
}

func TestCandidatesTruncateLongHumanPart(t *testing.T) {
	siteID := uuid.MustParse("018f0d5e-7c19-7abc-8d1e-1234567890ab")
	name := strings.Repeat("a", 63)
	candidates := Candidates(name, siteID)
	if len(candidates[1]) > MaxLabelLength {
		t.Fatalf("collision candidate length = %d, want <= %d", len(candidates[1]), MaxLabelLength)
	}
	if err := Validate(candidates[1]); err != nil {
		t.Fatalf("collision candidate is invalid: %v", err)
	}
}

func TestReservedLabelsAreNarrowAndCaseInsensitive(t *testing.T) {
	for _, label := range []string{"api", "ADMIN", "console", "status", "www"} {
		if !Reserved(label) {
			t.Fatalf("Reserved(%q) = false", label)
		}
	}
	if Reserved("blog") {
		t.Fatal("ordinary Site label was reserved")
	}
}

func TestValidate(t *testing.T) {
	for _, test := range []struct {
		label string
		valid bool
	}{
		{label: "portfolio", valid: true},
		{label: "a-1", valid: true},
		{label: "", valid: false},
		{label: "-portfolio", valid: false},
		{label: "portfolio-", valid: false},
		{label: strings.Repeat("a", 64), valid: false},
		{label: "Portfolio", valid: false},
	} {
		t.Run(test.label, func(t *testing.T) {
			err := Validate(test.label)
			if (err == nil) != test.valid {
				t.Fatalf("Validate(%q) error = %v, valid = %t", test.label, err, test.valid)
			}
		})
	}
}

func TestHostname(t *testing.T) {
	got, err := Hostname("portfolio", "Apps.Example.COM.")
	if err != nil {
		t.Fatal(err)
	}
	if got != "portfolio.apps.example.com" {
		t.Fatalf("Hostname() = %q", got)
	}
	if _, err := Hostname("portfolio", "example.com"); err != nil {
		t.Fatalf("valid hostname rejected: %v", err)
	}
	if _, err := Hostname("portfolio-", "example.com"); err == nil {
		t.Fatal("invalid label accepted")
	}
	for _, base := range []string{"", "com", "apps.example.com:443", "https://apps.example.com"} {
		if _, err := Hostname("portfolio", base); err == nil {
			t.Fatalf("invalid workload base accepted: %q", base)
		}
	}
}
