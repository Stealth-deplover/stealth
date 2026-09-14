package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Stealth-deplover/stealth/internal/setupstate"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest = errors.New("Cloudflare provisioning request is invalid")
	ErrConflict       = errors.New("Cloudflare provisioning conflicts with existing setup")
	ErrProvider       = errors.New("Cloudflare provisioning provider operation failed")
	ErrState          = errors.New("Cloudflare provisioning state operation failed")
)

type ProvisionRequest struct {
	AccountID string
	ZoneID    string
	Hostname  string
	Name      string
}

type ProvisionError struct {
	Kind  error
	Stage string
	Err   error
}

func (e *ProvisionError) Error() string {
	if e == nil {
		return "Cloudflare provisioning failed"
	}
	if e.Err == nil {
		return e.Kind.Error()
	}
	return fmt.Sprintf("Cloudflare %s: %v", e.Stage, e.Err)
}

func (e *ProvisionError) Unwrap() []error {
	if e == nil {
		return nil
	}
	if e.Err == nil {
		return []error{e.Kind}
	}
	return []error{e.Kind, e.Err}
}

func provisioningError(kind error, stage string, err error) error {
	return &ProvisionError{Kind: kind, Stage: stage, Err: err}
}

// Provision creates or reconciles the named Cloudflare Tunnel and its DNS
// record. Durable intent and provider IDs are saved before and after each
// external write so a retry can discover an already-created resource instead
// of creating a duplicate.
func Provision(ctx context.Context, store setupstate.Store, client Client, request ProvisionRequest) (setupstate.State, error) {
	if store == nil {
		return setupstate.State{}, provisioningError(ErrState, "state", errors.New("setup state is not configured"))
	}
	if client == nil {
		return setupstate.State{}, provisioningError(ErrProvider, "client", errors.New("Cloudflare client is not configured"))
	}
	normalized, err := normalizeProvisionRequest(request)
	if err != nil {
		return setupstate.State{}, provisioningError(ErrInvalidRequest, "input", err)
	}
	state, err := store.Load(ctx)
	if err != nil {
		return setupstate.State{}, provisioningError(ErrState, "load", err)
	}
	if setupComplete(state) {
		return setupstate.State{}, provisioningError(ErrConflict, "state", errors.New("installation is already in progress or complete"))
	}
	if err := validateBinding(state, normalized); err != nil {
		return setupstate.State{}, provisioningError(ErrConflict, "binding", err)
	}
	zones, err := client.ListZones(ctx, normalized.AccountID)
	if err != nil {
		return setupstate.State{}, provisioningError(ErrProvider, "zones", err)
	}
	if !zoneContainsHostname(zones, normalized.ZoneID, normalized.Hostname) {
		return setupstate.State{}, provisioningError(ErrInvalidRequest, "zone", errors.New("hostname is not inside the selected Cloudflare zone"))
	}

	binding := state.EffectiveCloudflareBinding()
	tunnelName := binding.TunnelName
	hadIntent := tunnelName != ""
	if tunnelName == "" && binding.TunnelID == "" {
		tunnelName = normalized.Name
		if tunnelName == "" {
			tunnelName = "stealth-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		}
	}
	if tunnelName == "" && binding.TunnelID == "" {
		return setupstate.State{}, provisioningError(ErrInvalidRequest, "input", errors.New("tunnel name is required"))
	}

	tunnelID := binding.TunnelID
	if tunnelID == "" {
		if !hadIntent {
			tunnels, listErr := client.ListTunnels(ctx, normalized.AccountID, tunnelName)
			if listErr != nil {
				return setupstate.State{}, provisioningError(ErrProvider, "tunnel lookup", listErr)
			}
			matches := matchingTunnels(tunnels, tunnelName)
			if len(matches) > 0 {
				return setupstate.State{}, provisioningError(ErrConflict, "tunnel lookup", errors.New("a Cloudflare tunnel already uses the requested name"))
			}
			state, err = saveIntent(ctx, store, normalized, tunnelName)
			if err != nil {
				return setupstate.State{}, err
			}
			// The atomic state update may observe an intent reserved by a
			// concurrent adapter between the initial load and this call. Use the
			// durable values returned by that update instead of the local random
			// candidate, and reconcile the reserved resource below.
			binding = state.EffectiveCloudflareBinding()
			tunnelName = binding.TunnelName
			tunnelID = binding.TunnelID
			hadIntent = tunnelName != ""
			if tunnelID == "" && hadIntent {
				tunnels, listErr := client.ListTunnels(ctx, normalized.AccountID, tunnelName)
				if listErr != nil {
					return setupstate.State{}, provisioningError(ErrProvider, "tunnel lookup", listErr)
				}
				matches := matchingTunnels(tunnels, tunnelName)
				if len(matches) > 1 {
					return setupstate.State{}, provisioningError(ErrConflict, "tunnel lookup", errors.New("multiple Cloudflare tunnels have the requested name"))
				}
				if len(matches) == 1 {
					tunnelID = strings.TrimSpace(matches[0].ID)
				}
			}
		} else {
			tunnels, listErr := client.ListTunnels(ctx, normalized.AccountID, tunnelName)
			if listErr != nil {
				return setupstate.State{}, provisioningError(ErrProvider, "tunnel lookup", listErr)
			}
			matches := matchingTunnels(tunnels, tunnelName)
			if len(matches) > 1 {
				return setupstate.State{}, provisioningError(ErrConflict, "tunnel lookup", errors.New("multiple Cloudflare tunnels have the requested name"))
			}
			if len(matches) == 1 {
				tunnelID = strings.TrimSpace(matches[0].ID)
			}
		}
		if tunnelID == "" {
			tunnel, createErr := client.CreateTunnel(ctx, normalized.AccountID, tunnelName)
			if createErr != nil {
				return setupstate.State{}, provisioningError(ErrProvider, "tunnel create", createErr)
			}
			tunnelID = strings.TrimSpace(tunnel.ID)
			if tunnelID == "" {
				return setupstate.State{}, provisioningError(ErrProvider, "tunnel create", errors.New("Cloudflare returned an empty tunnel ID"))
			}
		}
		if state.EffectiveCloudflareBinding().TunnelID == "" {
			state, err = saveTunnelID(ctx, store, normalized, tunnelName, tunnelID)
			if err != nil {
				return setupstate.State{}, err
			}
		}
	}

	ingress := []IngressRule{{Hostname: normalized.Hostname, Service: "http://proxy:80"}, {Service: "http_status:404"}}
	if err := client.ConfigureTunnel(ctx, normalized.AccountID, tunnelID, ingress); err != nil {
		return setupstate.State{}, provisioningError(ErrProvider, "tunnel configure", err)
	}

	if state.Secret("cloudflare_tunnel_token") == "" {
		tunnelToken, tokenErr := client.TunnelToken(ctx, normalized.AccountID, tunnelID)
		if tokenErr != nil {
			return setupstate.State{}, provisioningError(ErrProvider, "tunnel token", tokenErr)
		}
		tunnelToken = strings.TrimSpace(tunnelToken)
		if tunnelToken == "" {
			return setupstate.State{}, provisioningError(ErrProvider, "tunnel token", errors.New("Cloudflare returned an empty tunnel token"))
		}
		state, err = saveTunnelToken(ctx, store, tunnelToken)
		if err != nil {
			return setupstate.State{}, err
		}
	}

	desiredRecord := DNSRecord{Type: "CNAME", Name: normalized.Hostname, Content: tunnelID + ".cfargotunnel.com", Proxied: true, TTL: 1}
	records, listErr := client.ListDNSRecords(ctx, normalized.ZoneID, normalized.Hostname)
	if listErr != nil {
		return setupstate.State{}, provisioningError(ErrProvider, "DNS lookup", listErr)
	}
	recordID, err := reconcileDNSRecord(ctx, client, normalized.ZoneID, desiredRecord, records)
	if err != nil {
		return setupstate.State{}, err
	}
	state, err = store.Update(ctx, func(state *setupstate.State) error {
		if err := validateBinding(*state, normalized); err != nil {
			return provisioningError(ErrConflict, "state", err)
		}
		currentBinding := state.EffectiveCloudflareBinding()
		if currentBinding.TunnelID != "" && currentBinding.TunnelID != tunnelID {
			return provisioningError(ErrConflict, "state", errors.New("another Cloudflare tunnel was saved during provisioning"))
		}
		state.Draft.Hostname = normalized.Hostname
		state.Draft.PublicURL = "https://" + normalized.Hostname
		state.Draft.NetworkMode = "cloudflare_tunnel"
		state.Cloudflare.Binding = setupstate.CloudflareBinding{
			AccountID:  normalized.AccountID,
			ZoneID:     normalized.ZoneID,
			Hostname:   normalized.Hostname,
			TunnelName: tunnelName,
			TunnelID:   tunnelID,
			RecordID:   recordID,
		}
		state.Cloudflare.Connected = true
		state.Cloudflare.TokenValid = true
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return setupstate.State{}, err
		}
		return setupstate.State{}, provisioningError(ErrState, "save", err)
	}
	return state, nil
}

func normalizeProvisionRequest(request ProvisionRequest) (ProvisionRequest, error) {
	accountID, err := safeID(request.AccountID, "account")
	if err != nil {
		return ProvisionRequest{}, err
	}
	zoneID, err := safeID(request.ZoneID, "zone")
	if err != nil {
		return ProvisionRequest{}, err
	}
	hostname, err := setupstate.ValidateHostname(request.Hostname)
	if err != nil {
		return ProvisionRequest{}, err
	}
	name := strings.TrimSpace(request.Name)
	if len(name) > 120 || strings.ContainsAny(name, "\x00\r\n") {
		return ProvisionRequest{}, errors.New("tunnel name is invalid")
	}
	return ProvisionRequest{AccountID: accountID, ZoneID: zoneID, Hostname: hostname, Name: name}, nil
}

func setupComplete(state setupstate.State) bool {
	return state.Phase == setupstate.PhaseInstalling || state.Phase == setupstate.PhaseComplete || state.Phase == setupstate.PhaseHandoff
}

func validateBinding(state setupstate.State, request ProvisionRequest) error {
	if !state.Cloudflare.Binding.IsZero() {
		if err := state.Cloudflare.Binding.ValidateDraft(state.Draft); err != nil {
			return err
		}
	}
	return state.EffectiveCloudflareBinding().ValidateRequest(setupstate.CloudflareBinding{
		AccountID:  request.AccountID,
		ZoneID:     request.ZoneID,
		Hostname:   request.Hostname,
		TunnelName: request.Name,
	})
}

func saveIntent(ctx context.Context, store setupstate.Store, request ProvisionRequest, name string) (setupstate.State, error) {
	state, err := store.Update(ctx, func(state *setupstate.State) error {
		if setupComplete(*state) {
			return provisioningError(ErrConflict, "state", errors.New("installation is already in progress or complete"))
		}
		if err := validateBinding(*state, request); err != nil {
			return provisioningError(ErrConflict, "state", err)
		}
		if state.EffectiveCloudflareBinding().HasIntent() {
			// A competing request may have reserved or created the tunnel after
			// the caller's initial load. Preserve that durable intent/ID and let
			// the caller reconcile it rather than replacing it with a new name.
			if state.Draft.Hostname == "" {
				state.Draft.Hostname = request.Hostname
			}
			if state.Draft.PublicURL == "" {
				state.Draft.PublicURL = "https://" + request.Hostname
			}
			state.Cloudflare.Binding = state.EffectiveCloudflareBinding()
			return nil
		}
		state.Cloudflare.Binding = setupstate.CloudflareBinding{
			AccountID:  request.AccountID,
			ZoneID:     request.ZoneID,
			Hostname:   request.Hostname,
			TunnelName: name,
		}
		state.Draft.Hostname = request.Hostname
		state.Draft.PublicURL = "https://" + request.Hostname
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return setupstate.State{}, err
		}
		return setupstate.State{}, provisioningError(ErrState, "intent", err)
	}
	return state, nil
}

func saveTunnelID(ctx context.Context, store setupstate.Store, request ProvisionRequest, name, tunnelID string) (setupstate.State, error) {
	state, err := store.Update(ctx, func(state *setupstate.State) error {
		if err := validateBinding(*state, request); err != nil {
			return provisioningError(ErrConflict, "state", err)
		}
		currentBinding := state.EffectiveCloudflareBinding()
		if currentBinding.TunnelID != "" && currentBinding.TunnelID != tunnelID {
			return provisioningError(ErrConflict, "state", errors.New("another Cloudflare tunnel was saved during provisioning"))
		}
		binding := state.EffectiveCloudflareBinding()
		binding.AccountID = request.AccountID
		binding.ZoneID = request.ZoneID
		binding.Hostname = request.Hostname
		binding.TunnelName = name
		binding.TunnelID = tunnelID
		state.Cloudflare.Binding = binding
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return setupstate.State{}, err
		}
		return setupstate.State{}, provisioningError(ErrState, "tunnel ID", err)
	}
	return state, nil
}

func saveTunnelToken(ctx context.Context, store setupstate.Store, token string) (setupstate.State, error) {
	state, err := store.Update(ctx, func(state *setupstate.State) error {
		state.SetSecret("cloudflare_tunnel_token", token)
		return nil
	})
	if err != nil {
		return setupstate.State{}, provisioningError(ErrState, "tunnel token", err)
	}
	return state, nil
}

func matchingTunnels(tunnels []Tunnel, name string) []Tunnel {
	matches := make([]Tunnel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		if strings.TrimSpace(tunnel.ID) != "" && strings.TrimSpace(tunnel.Name) == name {
			matches = append(matches, tunnel)
		}
	}
	return matches
}

func reconcileDNSRecord(ctx context.Context, client Client, zoneID string, desired DNSRecord, records []DNSRecord) (string, error) {
	hostname := canonicalDNSName(desired.Name)
	target := canonicalDNSName(desired.Content)
	var matching DNSRecord
	for _, record := range records {
		if canonicalDNSName(record.Name) != hostname {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(record.Type)) != "CNAME" {
			return "", provisioningError(ErrConflict, "DNS lookup", errors.New("the requested hostname already has a non-CNAME DNS record"))
		}
		if canonicalDNSName(record.Content) != target {
			return "", provisioningError(ErrConflict, "DNS lookup", errors.New("the requested hostname already has a different CNAME record"))
		}
		if matching.ID != "" {
			return "", provisioningError(ErrConflict, "DNS lookup", errors.New("multiple matching DNS records exist for the requested hostname"))
		}
		matching = record
	}
	if matching.ID == "" {
		created, err := client.CreateDNSRecord(ctx, zoneID, desired)
		if err != nil {
			return "", provisioningError(ErrProvider, "DNS create", err)
		}
		if strings.TrimSpace(created.ID) == "" {
			return "", provisioningError(ErrProvider, "DNS create", errors.New("Cloudflare returned an empty DNS record ID"))
		}
		matching = created
	}
	if _, err := client.UpdateDNSRecord(ctx, zoneID, matching.ID, desired); err != nil {
		return "", provisioningError(ErrProvider, "DNS configure", err)
	}
	return strings.TrimSpace(matching.ID), nil
}

func canonicalDNSName(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func zoneContainsHostname(zones []Zone, zoneID, hostname string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	for _, zone := range zones {
		zoneName := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(zone.Name), "."))
		if zone.ID == zoneID && zoneName != "" && (hostname == zoneName || strings.HasSuffix(hostname, "."+zoneName)) {
			return true
		}
	}
	return false
}
