package httpapi

import (
	"testing"

	"github.com/Stealth-deplover/stealth/internal/config"
)

func TestValidAgentProviderModel(t *testing.T) {
	server := &Server{
		config: config.Config{
			AgentProviderCatalog: []config.AgentProviderCatalogItem{
				{ID: "local", Name: "Local", Models: []string{"model-a", "model-b"}},
				{ID: "remote", Name: "Remote", Models: []string{"model-x"}},
			},
		},
	}

	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{name: "valid pair", provider: "local", model: "model-a", want: true},
		{name: "model from another provider", provider: "local", model: "model-x"},
		{name: "unknown provider", provider: "missing", model: "model-a"},
		{name: "unknown model", provider: "remote", model: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := server.validAgentProviderModel(test.provider, test.model); got != test.want {
				t.Fatalf("validAgentProviderModel(%q, %q) = %v, want %v", test.provider, test.model, got, test.want)
			}
		})
	}
}
