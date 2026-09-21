package installengine

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
)

const (
	defaultIngressNetworkName = "stealth_ingress"
	defaultIngressSubnet      = "172.31.0.0/24"
	defaultIngressIPRange     = "172.31.0.64/26"
	defaultTraefikIngressIP   = "172.31.0.254"
	defaultCloudflaredIP      = "172.31.0.10"
)

var ingressNetworkEnvKeys = []string{
	"STEALTH_INGRESS_NETWORK_SUBNET",
	"STEALTH_INGRESS_IP_RANGE",
	"STEALTH_TRAEFIK_INGRESS_IP",
	"STEALTH_CLOUDFLARED_INGRESS_IP",
}

// IngressNetworkConfig is installation state, not a release default. The
// fixed peer addresses are deliberately outside IPRange so Docker cannot
// allocate them to an ordinary API, Console, or future workload container.
type IngressNetworkConfig struct {
	Name          string
	Subnet        string
	IPRange       string
	TraefikIP     string
	CloudflaredIP string
}

func defaultIngressNetworkConfig() IngressNetworkConfig {
	return IngressNetworkConfig{
		Name:          defaultIngressNetworkName,
		Subnet:        defaultIngressSubnet,
		IPRange:       defaultIngressIPRange,
		TraefikIP:     defaultTraefikIngressIP,
		CloudflaredIP: defaultCloudflaredIP,
	}
}

func ingressNetworkConfigFromValues(values map[string]string) (IngressNetworkConfig, error) {
	candidate := defaultIngressNetworkConfig()
	var err error
	candidate.Name = firstNonEmpty(strings.TrimSpace(values["STEALTH_INGRESS_NETWORK_NAME"]), candidate.Name)
	candidate.Subnet = firstNonEmpty(strings.TrimSpace(values["STEALTH_INGRESS_NETWORK_SUBNET"]), candidate.Subnet)

	// Derive omitted peer values from the configured subnet. This keeps older
	// config.env files upgradeable without silently changing an operator's
	// explicit subnet choice. Explicit peer values are parsed independently so
	// smaller custom subnets can still be used when their peers are supplied.
	if strings.TrimSpace(values["STEALTH_TRAEFIK_INGRESS_IP"]) == "" {
		defaultTraefik, err := ingressPeerForSubnet(candidate.Subnet, 254)
		if err != nil {
			return IngressNetworkConfig{}, err
		}
		candidate.TraefikIP = defaultTraefik
	} else {
		candidate.TraefikIP = strings.TrimSpace(values["STEALTH_TRAEFIK_INGRESS_IP"])
	}
	if strings.TrimSpace(values["STEALTH_CLOUDFLARED_INGRESS_IP"]) == "" {
		defaultCloudflared, err := ingressPeerForSubnet(candidate.Subnet, 10)
		if err != nil {
			return IngressNetworkConfig{}, err
		}
		candidate.CloudflaredIP = defaultCloudflared
	} else {
		candidate.CloudflaredIP = strings.TrimSpace(values["STEALTH_CLOUDFLARED_INGRESS_IP"])
	}
	if strings.TrimSpace(values["STEALTH_INGRESS_IP_RANGE"]) != "" {
		candidate.IPRange = strings.TrimSpace(values["STEALTH_INGRESS_IP_RANGE"])
	} else {
		candidate.IPRange, err = defaultIngressIPRangeForSubnet(candidate.Subnet)
		if err != nil {
			return IngressNetworkConfig{}, err
		}
	}
	if err := candidate.Validate(); err != nil {
		return IngressNetworkConfig{}, err
	}
	subnet, _, _, _ := parsePrivateIPv4CIDR(candidate.Subnet, "STEALTH_INGRESS_NETWORK_SUBNET")
	ipRange, _, _, _ := parsePrivateIPv4CIDR(candidate.IPRange, "STEALTH_INGRESS_IP_RANGE")
	candidate.Subnet = subnet.String()
	candidate.IPRange = ipRange.String()
	candidate.TraefikIP = net.ParseIP(candidate.TraefikIP).To4().String()
	candidate.CloudflaredIP = net.ParseIP(candidate.CloudflaredIP).To4().String()
	return candidate, nil
}

func ingressNetworkConfigFromOptions(options ConfigOptions) (IngressNetworkConfig, error) {
	values := map[string]string{
		"STEALTH_INGRESS_NETWORK_NAME":   defaultIngressNetworkName,
		"STEALTH_INGRESS_NETWORK_SUBNET": options.IngressNetworkSubnet,
		"STEALTH_INGRESS_IP_RANGE":       options.IngressIPRange,
		"STEALTH_TRAEFIK_INGRESS_IP":     options.TraefikIngressIP,
		"STEALTH_CLOUDFLARED_INGRESS_IP": options.CloudflaredIngressIP,
	}
	return ingressNetworkConfigFromValues(values)
}

func (config IngressNetworkConfig) Validate() error {
	if strings.TrimSpace(config.Name) == "" {
		return fmt.Errorf("ingress network name is required")
	}
	if !validDockerNetworkName(config.Name) {
		return fmt.Errorf("ingress network name must contain only Docker-safe letters, digits, dots, dashes, and underscores")
	}
	subnet, subnetIP, subnetPrefix, err := parsePrivateIPv4CIDR(config.Subnet, "STEALTH_INGRESS_NETWORK_SUBNET")
	if err != nil {
		return err
	}
	if subnetPrefix < 16 || subnetPrefix > 28 {
		return fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET must use a prefix between /16 and /28")
	}
	ipRange, _, ipRangePrefix, err := parsePrivateIPv4CIDR(config.IPRange, "STEALTH_INGRESS_IP_RANGE")
	if err != nil {
		return err
	}
	if ipRangePrefix <= subnetPrefix || !cidrContainsCIDR(subnet, ipRange) {
		return fmt.Errorf("STEALTH_INGRESS_IP_RANGE must be a smaller CIDR contained by STEALTH_INGRESS_NETWORK_SUBNET")
	}
	if ipRangePrefix > 29 {
		return fmt.Errorf("STEALTH_INGRESS_IP_RANGE must leave a usable Docker allocation pool")
	}

	traefikIP, err := parsePrivateIPv4Address(config.TraefikIP, "STEALTH_TRAEFIK_INGRESS_IP")
	if err != nil {
		return err
	}
	cloudflaredIP, err := parsePrivateIPv4Address(config.CloudflaredIP, "STEALTH_CLOUDFLARED_INGRESS_IP")
	if err != nil {
		return err
	}
	for field, ip := range map[string]net.IP{
		"STEALTH_TRAEFIK_INGRESS_IP":     traefikIP,
		"STEALTH_CLOUDFLARED_INGRESS_IP": cloudflaredIP,
	} {
		if !subnet.Contains(ip) {
			return fmt.Errorf("%s must be inside STEALTH_INGRESS_NETWORK_SUBNET", field)
		}
		if ip.Equal(subnetIP) || ip.Equal(lastIP(subnet)) {
			return fmt.Errorf("%s cannot be the ingress network or broadcast address", field)
		}
		if ipRange.Contains(ip) {
			return fmt.Errorf("%s must not be inside STEALTH_INGRESS_IP_RANGE", field)
		}
	}
	if traefikIP.Equal(cloudflaredIP) {
		return fmt.Errorf("STEALTH_TRAEFIK_INGRESS_IP and STEALTH_CLOUDFLARED_INGRESS_IP must be distinct")
	}
	return nil
}

func validDockerNetworkName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 63 {
		return false
	}
	for index, character := range name {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '_' || character == '-' || character == '.' {
			if index == 0 && (character == '_' || character == '-' || character == '.') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func parsePrivateIPv4CIDR(raw, field string) (*net.IPNet, net.IP, int, error) {
	ip, network, err := net.ParseCIDR(strings.TrimSpace(raw))
	if err != nil || ip == nil || ip.To4() == nil || network == nil {
		return nil, nil, 0, fmt.Errorf("%s must be a valid IPv4 CIDR", field)
	}
	ip = ip.To4()
	network.IP = network.IP.To4()
	one, bits := network.Mask.Size()
	if bits != 32 || !isRFC1918(ip) {
		return nil, nil, 0, fmt.Errorf("%s must be an RFC1918 IPv4 CIDR", field)
	}
	return network, network.IP, one, nil
}

func parsePrivateIPv4Address(raw, field string) (net.IP, error) {
	ip := net.ParseIP(strings.TrimSpace(raw))
	if ip == nil || ip.To4() == nil || !isRFC1918(ip.To4()) {
		return nil, fmt.Errorf("%s must be a valid RFC1918 IPv4 address", field)
	}
	return ip.To4(), nil
}

func isRFC1918(ip net.IP) bool {
	return ip != nil && (ip[0] == 10 || (ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31) || (ip[0] == 192 && ip[1] == 168))
}

func cidrContainsCIDR(parent, child *net.IPNet) bool {
	return parent.Contains(child.IP) && parent.Contains(lastIP(child))
}

func lastIP(network *net.IPNet) net.IP {
	last := network.IP.To4()
	result := make(net.IP, net.IPv4len)
	for index := range result {
		result[index] = last[index] | ^network.Mask[index]
	}
	return result
}

func ingressPeerForSubnet(raw string, hostByte byte) (string, error) {
	_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
	if err != nil || network == nil || network.IP.To4() == nil {
		return "", fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET must be a valid IPv4 CIDR")
	}
	base := network.IP.To4()
	if base[0] != 10 && !(base[0] == 172 && base[1] >= 16 && base[1] <= 31) && !(base[0] == 192 && base[1] == 168) {
		return "", fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET must be an RFC1918 IPv4 CIDR")
	}
	peer := net.IPv4(base[0], base[1], base[2], hostByte).To4()
	if !network.Contains(peer) {
		return "", fmt.Errorf("default ingress peer %s is outside STEALTH_INGRESS_NETWORK_SUBNET", peer)
	}
	return peer.String(), nil
}

func defaultIngressIPRangeForSubnet(raw string) (string, error) {
	_, network, err := net.ParseCIDR(strings.TrimSpace(raw))
	if err != nil || network == nil || network.IP.To4() == nil {
		return "", fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET must be a valid IPv4 CIDR")
	}
	prefix, bits := network.Mask.Size()
	if bits != 32 || prefix < 16 || prefix > 28 {
		return "", fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET must use a prefix between /16 and /28")
	}
	poolPrefix := 26
	if prefix >= 26 {
		poolPrefix = prefix + 1
	}
	if poolPrefix > 29 {
		return "", fmt.Errorf("STEALTH_INGRESS_NETWORK_SUBNET is too small for a dynamic ingress allocation pool")
	}
	base := network.IP.To4()
	if prefix < 26 {
		base[3] += 64
	}
	pool := net.IPv4(base[0], base[1], base[2], base[3]).To4()
	poolNetwork := &net.IPNet{IP: pool, Mask: net.CIDRMask(poolPrefix, 32)}
	poolNetwork.IP = poolNetwork.IP.Mask(poolNetwork.Mask)
	if !cidrContainsCIDR(network, poolNetwork) {
		return "", fmt.Errorf("could not derive a dynamic ingress allocation pool from STEALTH_INGRESS_NETWORK_SUBNET")
	}
	return poolNetwork.String(), nil
}

func setIngressNetworkValues(values map[string]string, config IngressNetworkConfig) {
	values["STEALTH_INGRESS_NETWORK_NAME"] = config.Name
	values["STEALTH_INGRESS_NETWORK_SUBNET"] = config.Subnet
	values["STEALTH_INGRESS_IP_RANGE"] = config.IPRange
	values["STEALTH_TRAEFIK_INGRESS_IP"] = config.TraefikIP
	values["STEALTH_CLOUDFLARED_INGRESS_IP"] = config.CloudflaredIP
}

func removeTrustedProxyCIDR(value, unwanted string) string {
	entries := strings.Split(strings.TrimSpace(value), ",")
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || entry == strings.TrimSpace(unwanted) {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, ",")
}

func (config IngressNetworkConfig) trustedProxyCIDR() string {
	return net.ParseIP(config.TraefikIP).To4().String() + "/32"
}

func networkConfigHasOperatorValues(values map[string]string) bool {
	defaults := defaultIngressNetworkConfig()
	if name := strings.TrimSpace(values["STEALTH_INGRESS_NETWORK_NAME"]); name != "" && name != defaults.Name {
		return true
	}
	if value := strings.TrimSpace(values["STEALTH_INGRESS_NETWORK_SUBNET"]); value != "" && value != defaults.Subnet {
		return true
	}
	if value := strings.TrimSpace(values["STEALTH_INGRESS_IP_RANGE"]); value != "" && value != defaults.IPRange {
		return true
	}
	if value := strings.TrimSpace(values["STEALTH_TRAEFIK_INGRESS_IP"]); value != "" && value != defaults.TraefikIP {
		return true
	}
	return strings.TrimSpace(values["STEALTH_CLOUDFLARED_INGRESS_IP"]) != "" && strings.TrimSpace(values["STEALTH_CLOUDFLARED_INGRESS_IP"]) != defaults.CloudflaredIP
}

func missingIngressNetworkValues(values map[string]string) bool {
	for _, key := range ingressNetworkEnvKeys {
		if strings.TrimSpace(values[key]) == "" {
			return true
		}
	}
	return false
}

type inspectedDockerNetwork struct {
	Name string `json:"Name"`
	IPAM struct {
		Config []struct {
			Subnet string `json:"Subnet"`
		} `json:"Config"`
	} `json:"IPAM"`
}

func (e *Engine) dockerNetworkSubnets(ctx context.Context, directory string) ([]inspectedDockerNetwork, error) {
	ids, err := e.runner.Output(ctx, directory, "docker", "network", "ls", "--format", "{{.ID}}")
	if err != nil {
		return nil, fmt.Errorf("inspect existing Docker networks: %w", err)
	}
	arguments := strings.Fields(string(ids))
	if len(arguments) == 0 {
		return nil, nil
	}
	inspected, err := e.runner.Output(ctx, directory, "docker", append([]string{"network", "inspect"}, arguments...)...)
	if err != nil {
		return nil, fmt.Errorf("inspect existing Docker network subnets: %w", err)
	}
	var networks []inspectedDockerNetwork
	if err := json.Unmarshal(inspected, &networks); err != nil {
		return nil, fmt.Errorf("decode Docker network inspection: %w", err)
	}
	return networks, nil
}

func dockerNetworkSubnetForName(networks []inspectedDockerNetwork, name string) (string, bool, error) {
	for _, network := range networks {
		if strings.TrimSpace(network.Name) != strings.TrimSpace(name) {
			continue
		}
		var subnet string
		for _, entry := range network.IPAM.Config {
			if strings.TrimSpace(entry.Subnet) == "" {
				continue
			}
			ip, parsed, err := net.ParseCIDR(strings.TrimSpace(entry.Subnet))
			if err != nil || ip.To4() == nil {
				continue
			}
			candidate := parsed.String()
			if subnet != "" && subnet != candidate {
				return "", false, fmt.Errorf("Docker network %q has multiple IPv4 subnets", name)
			}
			subnet = candidate
		}
		if subnet == "" {
			return "", false, fmt.Errorf("Docker network %q has no IPv4 subnet", name)
		}
		return subnet, true, nil
	}
	return "", false, nil
}

func ingressNetworkOverlaps(config IngressNetworkConfig, networks []inspectedDockerNetwork) (string, bool, error) {
	_, candidate, err := net.ParseCIDR(config.Subnet)
	if err != nil {
		return "", false, fmt.Errorf("parse selected ingress subnet: %w", err)
	}
	for _, network := range networks {
		if strings.TrimSpace(network.Name) == config.Name {
			continue
		}
		for _, entry := range network.IPAM.Config {
			if strings.TrimSpace(entry.Subnet) == "" {
				continue
			}
			ip, existing, parseErr := net.ParseCIDR(entry.Subnet)
			if parseErr != nil {
				return "", false, fmt.Errorf("decode Docker network %q subnet %q: %w", network.Name, entry.Subnet, parseErr)
			}
			if ip.To4() == nil {
				continue
			}
			if candidate.Contains(existing.IP) || existing.Contains(candidate.IP) {
				return network.Name + " (" + existing.String() + ")", true, nil
			}
		}
	}
	return "", false, nil
}

func dockerNetworkNameExists(networks []inspectedDockerNetwork, name string) bool {
	for _, network := range networks {
		if strings.TrimSpace(network.Name) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func candidateIngressNetworkConfigs(name string) []IngressNetworkConfig {
	defaults := defaultIngressNetworkConfig()
	candidates := make([]IngressNetworkConfig, 0, 48)
	for octet := byte(0); octet < 32; octet++ {
		values := map[string]string{
			"STEALTH_INGRESS_NETWORK_NAME":   name,
			"STEALTH_INGRESS_NETWORK_SUBNET": fmt.Sprintf("172.31.%d.0/24", octet),
		}
		candidate, err := ingressNetworkConfigFromValues(values)
		if err == nil {
			candidates = append(candidates, candidate)
		}
	}
	for octet := byte(0); octet < 16; octet++ {
		values := map[string]string{
			"STEALTH_INGRESS_NETWORK_NAME":   name,
			"STEALTH_INGRESS_NETWORK_SUBNET": fmt.Sprintf("10.250.%d.0/24", octet),
		}
		candidate, err := ingressNetworkConfigFromValues(values)
		if err == nil {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return []IngressNetworkConfig{defaults}
	}
	return candidates
}

func (e *Engine) resolveIngressNetworkConfig(ctx context.Context, plan Plan, values, original map[string]string) (IngressNetworkConfig, error) {
	current, err := ingressNetworkConfigFromValues(values)
	if err != nil {
		return IngressNetworkConfig{}, err
	}
	networks, err := e.dockerNetworkSubnets(ctx, plan.Layout.Root)
	if err != nil {
		return IngressNetworkConfig{}, err
	}
	autoSelect := !plan.Existing && !networkConfigHasOperatorValues(values)
	if plan.Existing && missingIngressNetworkValues(original) {
		if subnet, found, subnetErr := dockerNetworkSubnetForName(networks, current.Name); subnetErr != nil {
			return IngressNetworkConfig{}, subnetErr
		} else if found {
			// Pre-network-config installations did not persist the subnet. Adopt
			// the already-created named network instead of accidentally creating a
			// second network or changing the host route on upgrade.
			adopted := make(map[string]string, len(values))
			for key, value := range values {
				adopted[key] = value
			}
			for _, key := range ingressNetworkEnvKeys {
				if strings.TrimSpace(original[key]) == "" {
					delete(adopted, key)
				}
			}
			adopted["STEALTH_INGRESS_NETWORK_SUBNET"] = subnet
			current, err = ingressNetworkConfigFromValues(adopted)
			if err != nil {
				return IngressNetworkConfig{}, err
			}
			autoSelect = false
		} else {
			autoSelect = !networkConfigHasOperatorValues(original)
		}
	}
	if plan.Existing && !missingIngressNetworkValues(original) {
		if subnet, found, subnetErr := dockerNetworkSubnetForName(networks, current.Name); subnetErr != nil {
			return IngressNetworkConfig{}, subnetErr
		} else if found && subnet != current.Subnet {
			return IngressNetworkConfig{}, fmt.Errorf("configured Stealth ingress subnet %s does not match existing Docker network %q subnet %s", current.Subnet, current.Name, subnet)
		}
	}
	if !plan.Existing && dockerNetworkNameExists(networks, current.Name) {
		return IngressNetworkConfig{}, fmt.Errorf("Docker network %q already exists; choose a unique STEALTH_INGRESS_NETWORK_NAME for this installation", current.Name)
	}
	if autoSelect {
		candidates := candidateIngressNetworkConfigs(current.Name)
		for _, candidate := range candidates {
			if _, overlaps, overlapErr := ingressNetworkOverlaps(candidate, networks); overlapErr != nil {
				return IngressNetworkConfig{}, overlapErr
			} else if !overlaps {
				return candidate, nil
			}
		}
		return IngressNetworkConfig{}, fmt.Errorf("no free Stealth ingress subnet remains in the bounded candidate set; set STEALTH_INGRESS_NETWORK_SUBNET to an unused RFC1918 subnet")
	}
	if conflict, overlaps, err := ingressNetworkOverlaps(current, networks); err != nil {
		return IngressNetworkConfig{}, err
	} else if overlaps {
		return IngressNetworkConfig{}, fmt.Errorf("configured Stealth ingress subnet %s overlaps existing Docker network %s; choose a non-overlapping STEALTH_INGRESS_NETWORK_SUBNET", current.Subnet, conflict)
	}
	return current, nil
}
