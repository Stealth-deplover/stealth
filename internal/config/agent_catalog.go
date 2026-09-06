package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// AgentProviderCatalogItem is public provider metadata only. It deliberately
// contains no credentials and does not imply that a trusted Agent execution
// worker is installed.
type AgentProviderCatalogItem struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
}

var defaultAgentProviderCatalog = []AgentProviderCatalogItem{
	{ID: "openai", Name: "OpenAI", Models: []string{"GPT-5.6"}},
	{ID: "anthropic", Name: "Anthropic", Models: []string{"Claude Sonnet 4.5"}},
}

// DefaultAgentProviderCatalog returns a defensive copy suitable for a
// response or a hand-built Config in tests.
func DefaultAgentProviderCatalog() []AgentProviderCatalogItem {
	return cloneAgentProviderCatalog(defaultAgentProviderCatalog)
}

func parseAgentProviderCatalog(raw string) ([]AgentProviderCatalogItem, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultAgentProviderCatalog(), nil
	}
	var items []AgentProviderCatalogItem
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG must be a JSON array of provider metadata")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG must contain one JSON value")
	}
	if len(items) == 0 || len(items) > 32 {
		return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG must contain between 1 and 32 providers")
	}
	seenIDs := make(map[string]struct{}, len(items))
	for index := range items {
		item := &items[index]
		item.ID = strings.TrimSpace(item.ID)
		item.Name = strings.TrimSpace(item.Name)
		if !validCatalogText(item.ID, 1, 64, true) {
			return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG provider %d has an invalid id", index+1)
		}
		if !validCatalogText(item.Name, 1, 120, false) {
			return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG provider %d has an invalid name", index+1)
		}
		if _, exists := seenIDs[item.ID]; exists {
			return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG contains duplicate provider id %q", item.ID)
		}
		seenIDs[item.ID] = struct{}{}
		if len(item.Models) == 0 || len(item.Models) > 64 {
			return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG provider %q must contain between 1 and 64 models", item.ID)
		}
		seenModels := make(map[string]struct{}, len(item.Models))
		for modelIndex, model := range item.Models {
			model = strings.TrimSpace(model)
			if !validCatalogText(model, 1, 128, false) {
				return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG provider %q model %d is invalid", item.ID, modelIndex+1)
			}
			if _, exists := seenModels[model]; exists {
				return nil, fmt.Errorf("AGENT_PROVIDER_CATALOG provider %q contains duplicate model %q", item.ID, model)
			}
			seenModels[model] = struct{}{}
			item.Models[modelIndex] = model
		}
	}
	return cloneAgentProviderCatalog(items), nil
}

func cloneAgentProviderCatalog(items []AgentProviderCatalogItem) []AgentProviderCatalogItem {
	cloned := make([]AgentProviderCatalogItem, len(items))
	for index, item := range items {
		cloned[index] = item
		cloned[index].Models = append([]string(nil), item.Models...)
	}
	return cloned
}

func validCatalogText(value string, min, max int, token bool) bool {
	if value == "" || utf8.RuneCountInString(value) < min || utf8.RuneCountInString(value) > max || strings.ContainsAny(value, "\x00\t\r\n") {
		return false
	}
	if !token {
		return true
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
