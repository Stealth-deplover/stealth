package installengine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestIngressNetworkConfigValidation(t *testing.T) {
	valid := defaultIngressNetworkConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("default ingress network is invalid: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*IngressNetworkConfig)
	}{
		{name: "traefik outside subnet", mutate: func(config *IngressNetworkConfig) { config.TraefikIP = "10.0.0.1" }},
		{name: "cloudflared outside subnet", mutate: func(config *IngressNetworkConfig) { config.CloudflaredIP = "10.0.0.1" }},
		{name: "same peers", mutate: func(config *IngressNetworkConfig) { config.CloudflaredIP = config.TraefikIP }},
		{name: "network address", mutate: func(config *IngressNetworkConfig) { config.TraefikIP = "172.31.0.0" }},
		{name: "broadcast address", mutate: func(config *IngressNetworkConfig) { config.TraefikIP = "172.31.0.255" }},
		{name: "malformed subnet", mutate: func(config *IngressNetworkConfig) { config.Subnet = "172.31.0.0/garbage" }},
		{name: "IPv6 subnet", mutate: func(config *IngressNetworkConfig) { config.Subnet = "fd00::/64" }},
		{name: "IP range outside subnet", mutate: func(config *IngressNetworkConfig) { config.IPRange = "172.32.0.0/26" }},
		{name: "reserved peer in dynamic pool", mutate: func(config *IngressNetworkConfig) { config.IPRange = "172.31.0.0/25" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if err := config.Validate(); err == nil {
				t.Fatal("invalid ingress configuration was accepted")
			}
		})
	}

	custom, err := ingressNetworkConfigFromValues(map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   "custom_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET": "10.44.8.0/25",
		"STEALTH_INGRESS_IP_RANGE":       "10.44.8.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP":     "10.44.8.2",
		"STEALTH_CLOUDFLARED_INGRESS_IP": "10.44.8.3",
	})
	if err != nil {
		t.Fatalf("valid custom ingress network rejected: %v", err)
	}
	if custom.Subnet != "10.44.8.0/25" || custom.IPRange != "10.44.8.64/26" {
		t.Fatalf("custom ingress network was not canonicalized: %#v", custom)
	}
}

func TestIngressNetworkAutoSelectionUsesCIDROverlaps(t *testing.T) {
	tests := []struct {
		name       string
		networks   []testDockerNetwork
		wantSubnet string
		wantErr    string
	}{
		{name: "no collision", wantSubnet: "172.31.0.0/24"},
		{
			name:       "default occupied",
			networks:   []testDockerNetwork{{name: "other", subnet: "172.31.0.0/24"}},
			wantSubnet: "172.31.1.0/24",
		},
		{
			name:       "larger overlap",
			networks:   []testDockerNetwork{{name: "other", subnet: "172.31.0.0/16"}},
			wantSubnet: "10.250.0.0/24",
		},
		{
			name:       "smaller overlap",
			networks:   []testDockerNetwork{{name: "other", subnet: "172.31.0.128/25"}},
			wantSubnet: "172.31.1.0/24",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveTestIngressNetwork(t, false, test.networks, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Subnet != test.wantSubnet {
				t.Fatalf("selected subnet = %s, want %s", got.Subnet, test.wantSubnet)
			}
		})
	}

	allOccupied := make([]testDockerNetwork, 0, len(candidateIngressNetworkConfigs(defaultIngressNetworkName)))
	for index, candidate := range candidateIngressNetworkConfigs(defaultIngressNetworkName) {
		allOccupied = append(allOccupied, testDockerNetwork{name: fmt.Sprintf("occupied-%d", index), subnet: candidate.Subnet})
	}
	if _, err := resolveTestIngressNetwork(t, false, allOccupied, nil); err == nil || !strings.Contains(err.Error(), "no free Stealth ingress subnet") {
		t.Fatalf("all occupied candidates did not fail deterministically: %v", err)
	}
}

func TestIngressNetworkAutoSelectionIgnoresCustomNetworkName(t *testing.T) {
	values := map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME": "stealth_b_ingress",
	}
	got, err := resolveTestIngressNetwork(t, false, []testDockerNetwork{{name: "installation-a", subnet: defaultIngressSubnet}}, values)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "stealth_b_ingress" {
		t.Fatalf("selected network name = %q, want custom name", got.Name)
	}
	if got.Subnet != "172.31.1.0/24" || got.IPRange != "172.31.1.64/26" || got.TraefikIP != "172.31.1.254" || got.CloudflaredIP != "172.31.1.10" {
		t.Fatalf("custom-name installation did not select and derive the next free addressing set: %#v", got)
	}
}

func TestIngressNetworkExplicitCollisionAndNameLifecycle(t *testing.T) {
	values := map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   "custom_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET": "172.31.4.0/24",
		"STEALTH_INGRESS_IP_RANGE":       "172.31.4.64/26",
		"STEALTH_TRAEFIK_INGRESS_IP":     "172.31.4.254",
		"STEALTH_CLOUDFLARED_INGRESS_IP": "172.31.4.10",
	}
	if _, err := resolveTestIngressNetwork(t, false, []testDockerNetwork{{name: "other", subnet: "172.31.4.128/25"}}, values); err == nil || !strings.Contains(err.Error(), "overlaps existing Docker network") {
		t.Fatalf("explicit overlapping subnet did not fail: %v", err)
	}
	defaultValues := map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   "custom_default_ingress",
		"STEALTH_INGRESS_NETWORK_SUBNET": defaultIngressSubnet,
	}
	if _, err := resolveTestIngressNetwork(t, false, []testDockerNetwork{{name: "other", subnet: defaultIngressSubnet}}, defaultValues); err == nil || !strings.Contains(err.Error(), "overlaps existing Docker network") {
		t.Fatalf("explicit default subnet collision did not fail: %v", err)
	}
	if _, err := resolveTestIngressNetwork(t, false, []testDockerNetwork{{name: "custom_ingress", subnet: "172.31.4.0/24"}}, values); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("fresh install reused an existing network name: %v", err)
	}
	if _, err := resolveTestIngressNetwork(t, true, []testDockerNetwork{{name: "custom_ingress", subnet: "172.31.5.0/24"}}, values); err == nil || !strings.Contains(err.Error(), "does not match existing Docker network") {
		t.Fatalf("existing installation accepted a changed network subnet: %v", err)
	}
}

func TestIngressNetworkUpgradeAdoptsExistingNamedNetwork(t *testing.T) {
	values := map[string]string{"STEALTH_INGRESS_NETWORK_NAME": defaultIngressNetworkName, "STEALTH_INGRESS_NETWORK_SUBNET": defaultIngressSubnet}
	original := map[string]string{"STEALTH_INGRESS_NETWORK_NAME": defaultIngressNetworkName}
	got, err := resolveTestIngressNetwork(t, true, []testDockerNetwork{{name: defaultIngressNetworkName, subnet: "172.31.7.0/24"}}, values, original)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subnet != "172.31.7.0/24" || got.TraefikIP != "172.31.7.254" || got.CloudflaredIP != "172.31.7.10" {
		t.Fatalf("upgrade did not adopt existing ingress network: %#v", got)
	}
}

func TestRemoveTrustedProxyCIDRPreservesOtherPeers(t *testing.T) {
	if got := removeTrustedProxyCIDR("172.30.0.0/24, 172.31.0.254/32,10.0.0.0/8", "172.31.0.254/32"); got != "172.30.0.0/24,10.0.0.0/8" {
		t.Fatalf("remaining trusted peers = %q", got)
	}
	if got := removeTrustedProxyCIDR("172.30.0.0/24", "172.31.0.254/32"); got != "172.30.0.0/24" {
		t.Fatalf("unmatched trusted peers = %q", got)
	}
}

type testDockerNetwork struct {
	name   string
	subnet string
}

type ingressNetworkTestRunner struct {
	networks []testDockerNetwork
}

func (r ingressNetworkTestRunner) Run(context.Context, string, io.Writer, io.Writer, string, ...string) error {
	return nil
}

func (r ingressNetworkTestRunner) Output(_ context.Context, _ string, _ string, args ...string) ([]byte, error) {
	if len(args) >= 2 && args[0] == "network" && args[1] == "ls" {
		ids := make([]string, len(r.networks))
		for index := range r.networks {
			ids[index] = fmt.Sprintf("network-%d", index)
		}
		return []byte(strings.Join(ids, "\n")), nil
	}
	if len(args) >= 2 && args[0] == "network" && args[1] == "inspect" {
		payload := make([]map[string]any, 0, len(r.networks))
		for _, network := range r.networks {
			payload = append(payload, map[string]any{
				"Name": network.name,
				"IPAM": map[string]any{"Config": []map[string]string{{"Subnet": network.subnet}}},
			})
		}
		return json.Marshal(payload)
	}
	return nil, fmt.Errorf("unexpected Docker command: %v", args)
}

func resolveTestIngressNetwork(t *testing.T, existing bool, networks []testDockerNetwork, args ...map[string]string) (IngressNetworkConfig, error) {
	t.Helper()
	values := map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME": defaultIngressNetworkName,
	}
	if len(args) > 0 && args[0] != nil {
		values = args[0]
	}
	original := values
	if len(args) > 1 && args[1] != nil {
		original = args[1]
	}
	layout, err := NewLayout(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	engine := New(Options{Runner: ingressNetworkTestRunner{networks: networks}})
	return engine.resolveIngressNetworkConfig(context.Background(), Plan{Layout: layout, Existing: existing}, values, original)
}
