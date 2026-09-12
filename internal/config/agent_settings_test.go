package config

import "testing"

func TestLoadAgentSettings(t *testing.T) {
	t.Setenv("AGENT_PROVIDER_CATALOG", `[{"id":"local","name":"Local gateway","models":["model-a"]}]`)
	settings, err := loadAgentSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.providerCatalog) != 1 || settings.providerCatalog[0].ID != "local" {
		t.Fatalf("unexpected agent settings: %#v", settings)
	}
	var config Config
	settings.apply(&config)
	config.AgentProviderCatalog[0].Models[0] = "mutated"
	if settings.providerCatalog[0].Models[0] == "mutated" {
		t.Fatal("applying agent settings did not clone the catalog")
	}
}
