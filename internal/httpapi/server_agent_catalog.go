package httpapi

import (
	"net/http"

	"github.com/nazxf/stealth-api/internal/config"
	"github.com/nazxf/stealth-api/internal/repository"
)

// agentCatalogExecution describes the current control-plane boundary. The
// API accepts and persists runs, but never claims that a provider call or
// repository mutation happened until a trusted worker is deployed.
type agentCatalogExecution struct {
	Mode    string `json:"mode"`
	Ready   bool   `json:"ready"`
	Message string `json:"message"`
}

func (s *Server) agentCatalog(w http.ResponseWriter, _ *http.Request) {
	providers := s.config.AgentProviderCatalog
	if len(providers) == 0 {
		providers = config.DefaultAgentProviderCatalog()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": providers,
		"roles":     repository.SupportedAgentRoles(),
		"tools":     repository.SupportedAgentTools(),
		"execution": agentCatalogExecution{
			Mode:    "queue_only",
			Ready:   false,
			Message: "Runs are accepted into the durable queue; provider execution is not enabled on this installation.",
		},
	})
}
