package setupstate

import (
	"errors"
	"fmt"
	"strings"
)

// CloudflareBinding is the durable identity of the Cloudflare resources that
// belong to the selected production hostname. Draft contains the public
// compatibility projection; this value is the provider-side binding snapshot
// used to prevent a resource from being silently attached to a new hostname,
// account, or zone.
type CloudflareBinding struct {
	AccountID  string `json:"account_id,omitempty"`
	ZoneID     string `json:"zone_id,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	TunnelName string `json:"tunnel_name,omitempty"`
	TunnelID   string `json:"tunnel_id,omitempty"`
	RecordID   string `json:"record_id,omitempty"`
}

// CloudflareBindingConflict reports a requested mutation that would detach a
// provisioned or durably reserved Cloudflare resource from its binding.
type CloudflareBindingConflict struct {
	Existing CloudflareBinding
	Field    string
}

func (e *CloudflareBindingConflict) Error() string {
	if e == nil {
		return "Cloudflare Tunnel binding conflicts with the requested setup"
	}
	existing := e.Existing.Normalized()
	hostname := existing.Hostname
	if hostname == "" {
		hostname = "the existing production hostname"
	}
	field := "Cloudflare binding"
	switch e.Field {
	case "hostname":
		field = "production hostname"
	case "account":
		field = "Cloudflare account"
	case "zone":
		field = "Cloudflare zone"
	case "network":
		field = "production network mode"
	case "tunnel_name":
		field = "Cloudflare tunnel name"
	case "tunnel_id":
		field = "Cloudflare tunnel resource"
	case "record_id":
		field = "Cloudflare DNS resource"
	}
	return fmt.Sprintf("Cloudflare Tunnel is already configured for %s. Reconfigure Cloudflare networking before changing the %s.", hostname, field)
}

// CloudflareBindingFromDraft converts the current configuration hostname to a
// binding selector. Provider resource identities live only in CloudflareState.
func CloudflareBindingFromDraft(draft Draft) CloudflareBinding {
	return CloudflareBinding{
		Hostname: canonicalHostname(draft.Hostname),
	}
}

func cloudflareBindingFromLegacyDraft(draft legacyDraft) CloudflareBinding {
	return CloudflareBinding{
		AccountID:  strings.TrimSpace(draft.CloudflareAccountID),
		ZoneID:     strings.TrimSpace(draft.CloudflareZoneID),
		TunnelName: strings.TrimSpace(draft.CloudflareTunnelName),
		TunnelID:   strings.TrimSpace(draft.CloudflareTunnelID),
		RecordID:   strings.TrimSpace(draft.CloudflareRecordID),
	}
}

// EffectiveCloudflareBinding returns the provider snapshot when present and
// falls back to the current Draft hostname while an unprovisioned setup is
// still being configured.
func (s State) EffectiveCloudflareBinding() CloudflareBinding {
	if !s.Cloudflare.Binding.IsZero() {
		return s.Cloudflare.Binding.Normalized()
	}
	return CloudflareBindingFromDraft(s.Draft)
}

func (b CloudflareBinding) Normalized() CloudflareBinding {
	return CloudflareBinding{
		AccountID:  strings.TrimSpace(b.AccountID),
		ZoneID:     strings.TrimSpace(b.ZoneID),
		Hostname:   canonicalHostname(b.Hostname),
		TunnelName: strings.TrimSpace(b.TunnelName),
		TunnelID:   strings.TrimSpace(b.TunnelID),
		RecordID:   strings.TrimSpace(b.RecordID),
	}
}

func (b CloudflareBinding) IsZero() bool {
	b = b.Normalized()
	return b == (CloudflareBinding{})
}

func (b CloudflareBinding) Validate() error {
	b = b.Normalized()
	if len(b.AccountID) > 128 || len(b.ZoneID) > 128 || len(b.TunnelID) > 128 || len(b.RecordID) > 128 || len(b.TunnelName) > 120 || len(b.Hostname) > 253 {
		return errors.New("setup state Cloudflare binding is invalid")
	}
	for _, value := range []string{b.AccountID, b.ZoneID, b.TunnelName, b.TunnelID, b.RecordID} {
		if strings.ContainsAny(value, "\x00\r\n") {
			return errors.New("setup state Cloudflare binding is invalid")
		}
	}
	if b.Hostname != "" {
		if _, err := ValidateHostname(b.Hostname); err != nil {
			return fmt.Errorf("setup state Cloudflare binding hostname is invalid: %w", err)
		}
	}
	return nil
}

// HasIntent is true once setup has durably reserved a tunnel name or any
// provider resource identity. A reservation is treated as binding-affecting
// because a provider write may have succeeded before its ID was persisted.
func (b CloudflareBinding) HasIntent() bool {
	b = b.Normalized()
	return b.TunnelName != "" || b.TunnelID != "" || b.RecordID != ""
}

// ValidateRequest compares only the selectors a provisioning request is
// allowed to change. Resource IDs are intentionally not compared because a
// retry may be recovering an ID after an external write succeeded.
func (b CloudflareBinding) ValidateRequest(request CloudflareBinding) error {
	existing := b.Normalized()
	requested := request.Normalized()
	if !existing.HasIntent() {
		return nil
	}
	checks := []struct {
		field     string
		existing  string
		requested string
	}{
		{field: "account", existing: existing.AccountID, requested: requested.AccountID},
		{field: "zone", existing: existing.ZoneID, requested: requested.ZoneID},
		{field: "hostname", existing: existing.Hostname, requested: requested.Hostname},
	}
	for _, check := range checks {
		if check.existing != "" && check.existing != check.requested {
			return &CloudflareBindingConflict{Existing: existing, Field: check.field}
		}
	}
	if existing.TunnelName != "" && requested.TunnelName != "" && existing.TunnelName != requested.TunnelName {
		return &CloudflareBindingConflict{Existing: existing, Field: "tunnel_name"}
	}
	return nil
}

// ValidateDraft makes install/status validation fail if the provider snapshot
// and the currently selected Draft no longer describe the same hostname. The
// account, zone, and resource IDs are canonical in CloudflareBinding and are
// intentionally not duplicated in Draft.
func (b CloudflareBinding) ValidateDraft(draft Draft) error {
	existing := b.Normalized()
	if existing.IsZero() {
		return nil
	}
	current := CloudflareBindingFromDraft(draft)
	if existing.Hostname != current.Hostname {
		return &CloudflareBindingConflict{Existing: existing, Field: "hostname"}
	}
	return nil
}

func canonicalHostname(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}
