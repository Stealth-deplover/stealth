package config

import "os"

// agentSettings owns the public, non-secret provider/model catalog exposed to
// the Console. It intentionally does not load provider credentials or imply
// that an execution worker is available.
type agentSettings struct {
	providerCatalog []AgentProviderCatalogItem
}

func loadAgentSettings() (agentSettings, error) {
	providerCatalog, err := parseAgentProviderCatalog(os.Getenv("AGENT_PROVIDER_CATALOG"))
	if err != nil {
		return agentSettings{}, err
	}
	return agentSettings{providerCatalog: providerCatalog}, nil
}

func (s agentSettings) apply(c *Config) {
	c.AgentProviderCatalog = cloneAgentProviderCatalog(s.providerCatalog)
}
