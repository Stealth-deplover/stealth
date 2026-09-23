package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Stealth-deplover/stealth/internal/domainname"
)

const (
	EdgeTLSNotApplicable  = "not_applicable"
	EdgeTLSPending        = "pending"
	EdgeTLSReady          = "ready"
	EdgeTLSActionRequired = "action_required"
	EdgeTLSError          = "error"
)

type edgeTLSObservation struct {
	Status string
	Reason string
}

func inspectWorkloadEdgeTLS(ctx context.Context, client Client, zone Zone, workloadBaseDomain string) (edgeTLSObservation, error) {
	workloadBaseDomain, err := domainname.NormalizeDomain(workloadBaseDomain)
	if err != nil {
		return edgeTLSObservation{Status: EdgeTLSError, Reason: "The configured workload domain is invalid for Cloudflare edge certificate inspection."}, errors.New("invalid workload domain for edge TLS inspection")
	}
	workloadWildcard := "*." + workloadBaseDomain
	packs, err := client.ListCertificatePacks(ctx, zone.ID)
	if err != nil {
		return edgeTLSObservation{Status: EdgeTLSError, Reason: "Cloudflare edge certificate inventory could not be inspected. Grant SSL and Certificates Read access for the workload zone and retry."}, fmt.Errorf("inspect Cloudflare workload edge certificates: %w", err)
	}

	// Read Total TLS state for operator context, but never treat its enabled bit
	// as coverage. Cloudflare documents that Total TLS does not issue
	// certificates for hostnames used with Cloudflare Tunnel.
	totalTLS, _ := client.TotalTLSSettings(ctx, zone.ID)

	active, pending := false, false
	for _, pack := range packs {
		if strings.EqualFold(strings.TrimSpace(pack.Type), "total_tls") {
			continue
		}
		packStatus := strings.ToLower(strings.TrimSpace(pack.Status))
		for _, certificate := range pack.Certificates {
			covers := certificateHostsCoverWildcard(certificate.Hosts, workloadWildcard)
			if !covers {
				continue
			}
			certificateStatus := strings.ToLower(strings.TrimSpace(certificate.Status))
			if packStatus == "active" && certificateStatus == "active" {
				usable, expiryErr := certificateNotExpired(certificate.ExpiresOn, time.Now().UTC())
				if expiryErr != nil {
					return edgeTLSObservation{Status: EdgeTLSError, Reason: "Cloudflare returned incomplete certificate validity data; edge TLS coverage could not be proven."}, errors.New("Cloudflare returned invalid edge certificate expiration data")
				}
				if usable {
					active = true
				}
			}
			if isPendingCertificateStatus(packStatus) || isPendingCertificateStatus(certificateStatus) {
				pending = true
			}
		}
		// Pending packs can advertise the requested SANs before an individual
		// certificate object exists. Pack hosts are considered only for known
		// provisioning states, never as active certificate proof.
		if certificateHostsCoverWildcard(pack.Hosts, workloadWildcard) && isPendingCertificateStatus(packStatus) {
			pending = true
		}
	}
	if active {
		return edgeTLSObservation{Status: EdgeTLSReady}, nil
	}
	if pending {
		return edgeTLSObservation{
			Status: EdgeTLSPending,
			Reason: fmt.Sprintf("Cloudflare is still provisioning a production edge certificate for %s.", workloadWildcard),
		}, nil
	}
	if totalTLS.Enabled != nil && *totalTLS.Enabled {
		return edgeTLSObservation{
			Status: EdgeTLSActionRequired,
			Reason: fmt.Sprintf("No active production certificate covers %s. Total TLS does not issue certificates for Cloudflare Tunnel hostnames; configure an active edge certificate with this wildcard coverage or use a dedicated Cloudflare zone whose active certificate covers it.", workloadWildcard),
		}, nil
	}
	zoneDescription := strings.TrimSpace(zone.Type)
	if zoneDescription == "" {
		zoneDescription = "Cloudflare"
	} else {
		zoneDescription += " Cloudflare"
	}
	return edgeTLSObservation{
		Status: EdgeTLSActionRequired,
		Reason: fmt.Sprintf("No active production edge certificate covers %s in the %s zone %s. A certificate for a parent wildcard does not cover this deeper wildcard; configure active certificate coverage for the workload wildcard or use a dedicated Cloudflare zone whose active certificate covers it.", workloadWildcard, zoneDescription, zone.Name),
	}, nil
}

func certificateHostsCoverWildcard(hosts []string, requiredWildcard string) bool {
	required, ok := normalizeCertificatePattern(requiredWildcard)
	if !ok || !strings.HasPrefix(required, "*.") {
		return false
	}
	base := strings.TrimPrefix(required, "*.")
	for _, hostname := range hosts {
		pattern, ok := normalizeCertificatePattern(hostname)
		if !ok || pattern != required {
			continue
		}
		// Confirm the SAN's wildcard follows the one-label DNS rule as an
		// additional guard against accepting malformed provider host patterns.
		if certificatePatternMatchesHostname(pattern, "stealth-check."+base) {
			return true
		}
	}
	return false
}

func certificatePatternMatchesHostname(pattern, hostname string) bool {
	pattern, ok := normalizeCertificatePattern(pattern)
	if !ok {
		return false
	}
	hostname, err := domainname.NormalizeHostname(hostname)
	if err != nil {
		return false
	}
	if !strings.HasPrefix(pattern, "*.") {
		return pattern == hostname
	}
	baseLabels := strings.Split(strings.TrimPrefix(pattern, "*."), ".")
	hostLabels := strings.Split(hostname, ".")
	if len(hostLabels) != len(baseLabels)+1 {
		return false
	}
	for i := range baseLabels {
		if hostLabels[i+1] != baseLabels[i] {
			return false
		}
	}
	return hostLabels[0] != "" && hostLabels[0] != "*"
}

func normalizeCertificatePattern(value string) (string, bool) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	if strings.HasPrefix(value, "*.") {
		if strings.Contains(value[2:], "*") {
			return "", false
		}
		base, err := domainname.NormalizeDomain(value[2:])
		if err != nil {
			return "", false
		}
		return "*." + base, true
	}
	if strings.Contains(value, "*") {
		return "", false
	}
	hostname, err := domainname.NormalizeHostname(value)
	if err != nil {
		return "", false
	}
	return hostname, true
}

func isPendingCertificateStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "pending" || status == "initializing" || status == "authorizing" || status == "issuing" || strings.HasPrefix(status, "pending_")
}

func certificateNotExpired(expiresOn string, now time.Time) (bool, error) {
	if strings.TrimSpace(expiresOn) == "" {
		return true, nil
	}
	expires, err := time.Parse(time.RFC3339, strings.TrimSpace(expiresOn))
	if err != nil {
		return false, err
	}
	return expires.After(now), nil
}
