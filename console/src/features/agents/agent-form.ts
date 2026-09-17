import type { Agent, AgentCatalog } from "@/api/types";
import type { components } from "@/api/generated/schema";
import type { CreateField } from "@/components/create-dialog";
import {
  agentModelAfterProviderChange,
  agentModelOptions,
  agentProvider,
  defaultAgentModel,
  isValidAgentProviderModel,
} from "@/features/agents/agent-configuration";

export type AgentFormValues = {
  name: string;
  description: string;
  role: components["schemas"]["AgentRole"];
  provider: string;
  model: string;
  branch: string;
  tools: string;
  instructions: string;
};

export type AgentFormPayload = Pick<
  components["schemas"]["CreateAgentRequest"],
  | "name"
  | "description"
  | "role"
  | "provider"
  | "model"
  | "branch"
  | "tools"
  | "instructions"
>;

function optionalText(value: string) {
  const trimmed = value.trim();
  return trimmed || null;
}

function parseAgentTools(
  catalog: AgentCatalog,
  value: string,
): components["schemas"]["AgentTool"][] {
  const tools = value
    .split(",")
    .map((tool) => tool.trim())
    .filter(Boolean);
  const available = new Set(catalog.tools);
  const unsupported = tools.find(
    (tool) => !available.has(tool as components["schemas"]["AgentTool"]),
  );
  if (unsupported) {
    throw new Error("Select tools published by the server catalog.");
  }
  return tools as components["schemas"]["AgentTool"][];
}

export function agentFields(
  catalog: AgentCatalog | undefined,
  current?: Agent,
): CreateField<AgentFormValues>[] {
  const providers = catalog?.providers ?? [];
  const selectedProvider =
    agentProvider(catalog, current?.provider) ?? providers[0];

  return [
    {
      name: "name",
      label: "Name",
      placeholder: "frontend-reviewer",
      defaultValue: current?.name ?? "",
    },
    {
      name: "description",
      label: "Description",
      type: "textarea",
      required: false,
      defaultValue: current?.description ?? "",
      placeholder: "What should this task runner own?",
    },
    {
      name: "role",
      label: "Role",
      type: "select",
      defaultValue: current?.role ?? catalog?.roles[0] ?? "",
      options: (catalog?.roles ?? []).map((role) => ({
        value: role,
        label: role,
      })),
    },
    {
      name: "provider",
      label: "Provider",
      type: "select",
      defaultValue: current?.provider ?? selectedProvider?.id ?? "",
      options: providers.map((provider) => ({
        value: provider.id,
        label: provider.name,
      })),
      onChange: (value, values) => ({
        model: agentModelAfterProviderChange(catalog, value, values.model),
      }),
    },
    {
      name: "model",
      label: "Model",
      type: "select",
      defaultValue: defaultAgentModel(
        catalog,
        selectedProvider?.id,
        current?.model,
      ),
      optionsForValues: (values) =>
        agentModelOptions(catalog, values.provider).map((model) => ({
          value: model,
          label: model,
        })),
      help: "Provider and model values come from the server catalog; selections are sent unchanged to the Go API.",
    },
    {
      name: "branch",
      label: "Branch",
      defaultValue: current?.branch ?? "main",
      placeholder: "main",
    },
    {
      name: "tools",
      label: "Tools",
      type: "multiselect",
      required: false,
      defaultValue: current?.tools.join(",") ?? "",
      options: (catalog?.tools ?? []).map((tool) => ({
        value: tool,
        label: tool,
      })),
      help: "Only tools published by the server catalog can be selected.",
    },
    {
      name: "instructions",
      label: "Instructions",
      type: "textarea",
      required: false,
      defaultValue: current?.instructions ?? "",
      placeholder: "Optional execution instructions",
    },
  ];
}

export function agentPayload(
  catalog: AgentCatalog | undefined,
  values: AgentFormValues,
): AgentFormPayload {
  if (!catalog?.roles.includes(values.role)) {
    throw new Error("Select a role published by the server catalog.");
  }
  const provider = values.provider.trim();
  const model = values.model.trim();
  if (!isValidAgentProviderModel(catalog, provider, model)) {
    throw new Error("Select a model supported by the selected provider.");
  }
  const name = values.name.trim();
  if (!name) throw new Error("Name is required.");

  return {
    name,
    description: values.description.trim(),
    role: values.role,
    provider,
    model,
    branch: values.branch.trim(),
    tools: parseAgentTools(catalog, values.tools),
    instructions: optionalText(values.instructions),
  };
}
