import type { AgentCatalog } from "@/api/types";

export function agentProvider(
  catalog: AgentCatalog | undefined,
  providerId: string | undefined,
) {
  return catalog?.providers.find((provider) => provider.id === providerId);
}

export function agentModelOptions(
  catalog: AgentCatalog | undefined,
  providerId: string | undefined,
) {
  return agentProvider(catalog, providerId)?.models ?? [];
}

export function defaultAgentModel(
  catalog: AgentCatalog | undefined,
  providerId: string | undefined,
  currentModel?: string,
) {
  const models = agentModelOptions(catalog, providerId);
  if (currentModel && models.includes(currentModel)) return currentModel;
  return models[0] ?? "";
}

export function agentModelAfterProviderChange(
  catalog: AgentCatalog | undefined,
  providerId: string,
  currentModel: string | undefined,
) {
  const models = agentModelOptions(catalog, providerId);
  if (currentModel && models.includes(currentModel)) return currentModel;
  return models.length === 1 ? models[0] : "";
}

export function isValidAgentProviderModel(
  catalog: AgentCatalog | undefined,
  providerId: string | undefined,
  model: string | undefined,
) {
  return Boolean(
    providerId &&
    model &&
    agentModelOptions(catalog, providerId).includes(model),
  );
}
