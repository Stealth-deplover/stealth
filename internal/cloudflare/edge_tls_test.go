package cloudflare

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInspectWorkloadEdgeTLS(t *testing.T) {
	cases := []struct {
		name       string
		zone       Zone
		packs      []CertificatePack
		packsErr   error
		wantStatus string
		wantReason string
	}{
		{
			name: "dedicated workload zone with active wildcard",
			zone: Zone{ID: "apps-zone", Name: "apps.example.com", Type: "full"},
			packs: []CertificatePack{{
				ID: "pack-1", Type: "universal", Status: "active", Hosts: []string{"apps.example.com", "*.apps.example.com"},
				Certificates: []Certificate{{ID: "cert-1", Status: "active", Hosts: []string{"apps.example.com", "*.apps.example.com"}}},
			}},
			wantStatus: EdgeTLSReady,
		},
		{
			name: "parent wildcard does not cover deeper workload wildcard",
			zone: Zone{ID: "example-zone", Name: "example.com", Type: "full"},
			packs: []CertificatePack{{
				ID: "pack-1", Type: "universal", Status: "active", Hosts: []string{"example.com", "*.example.com"},
				Certificates: []Certificate{{ID: "cert-1", Status: "active", Hosts: []string{"*.example.com"}}},
			}},
			wantStatus: EdgeTLSActionRequired,
			wantReason: "*.apps.example.com",
		},
		{
			name: "active certificate with exact workload wildcard",
			zone: Zone{ID: "example-zone", Name: "example.com", Type: "full"},
			packs: []CertificatePack{{
				ID: "pack-1", Type: "advanced", Status: "active", Hosts: []string{"example.com", "*.apps.example.com"},
				Certificates: []Certificate{{ID: "cert-1", Status: "active", Hosts: []string{"*.apps.example.com"}}},
			}},
			wantStatus: EdgeTLSReady,
		},
		{
			name: "certificate provisioning",
			zone: Zone{ID: "apps-zone", Name: "apps.example.com", Type: "full"},
			packs: []CertificatePack{{
				ID: "pack-1", Type: "advanced", Status: "pending_deployment", Hosts: []string{"apps.example.com", "*.apps.example.com"},
			}},
			wantStatus: EdgeTLSPending,
			wantReason: "provisioning",
		},
		{
			name:       "certificate API unavailable",
			zone:       Zone{ID: "apps-zone", Name: "apps.example.com", Type: "full"},
			packsErr:   errors.New("provider error"),
			wantStatus: EdgeTLSError,
			wantReason: "could not be inspected",
		},
		{
			name: "irrelevant active certificate",
			zone: Zone{ID: "example-zone", Name: "example.com", Type: "full"},
			packs: []CertificatePack{{
				ID: "pack-1", Type: "advanced", Status: "active", Hosts: []string{"example.com", "*.other.example.com"},
				Certificates: []Certificate{{ID: "cert-1", Status: "active", Hosts: []string{"*.other.example.com"}}},
			}},
			wantStatus: EdgeTLSActionRequired,
			wantReason: "*.apps.example.com",
		},
		{
			name:       "Total TLS alone is not proof for a Tunnel hostname",
			zone:       Zone{ID: "example-zone", Name: "example.com", Type: "full"},
			wantStatus: EdgeTLSActionRequired,
			wantReason: "Total TLS does not issue certificates for Cloudflare Tunnel hostnames",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := &routingFakeClient{certificatePacks: testCase.packs, certificatePacksErr: testCase.packsErr}
			if testCase.name == "Total TLS alone is not proof for a Tunnel hostname" {
				enabled := true
				client.totalTLS = TotalTLSSettings{Enabled: &enabled}
			}
			observation, err := inspectWorkloadEdgeTLS(context.Background(), client, testCase.zone, "apps.example.com")
			if observation.Status != testCase.wantStatus {
				t.Fatalf("edge TLS status = %q, want %q (reason %q)", observation.Status, testCase.wantStatus, observation.Reason)
			}
			if testCase.packsErr != nil && err == nil {
				t.Fatal("certificate API failure returned no error")
			}
			if testCase.packsErr == nil && err != nil {
				t.Fatalf("inspectWorkloadEdgeTLS() error = %v", err)
			}
			if testCase.wantReason != "" && !strings.Contains(observation.Reason, testCase.wantReason) {
				t.Fatalf("edge TLS reason = %q, want it to contain %q", observation.Reason, testCase.wantReason)
			}
		})
	}
}

func TestCertificateWildcardCoverageUsesExactlyOneLabelAndCanonicalIDNs(t *testing.T) {
	pattern := "*.apps.example.com"
	for _, hostname := range []string{"portfolio.apps.example.com", "blog.apps.example.com", "docs.apps.example.com"} {
		if !certificatePatternMatchesHostname(pattern, hostname) {
			t.Errorf("%s should match %s", hostname, pattern)
		}
	}
	for _, hostname := range []string{"foo.bar.apps.example.com", "apps.example.com", "example.com"} {
		if certificatePatternMatchesHostname(pattern, hostname) {
			t.Errorf("%s must not match %s", hostname, pattern)
		}
	}
	if !certificatePatternMatchesHostname("*.xn--bcher-kva.example", "blog.bücher.example") {
		t.Fatal("IDN certificate coverage must compare canonical punycode hostnames")
	}
	if !certificateHostsCoverWildcard([]string{"*.bücher.example"}, "*.xn--bcher-kva.example") {
		t.Fatal("Unicode SAN must cover the equivalent canonical wildcard")
	}
}
