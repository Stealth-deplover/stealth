import { describe, expect, it } from "vitest";
import {
  AgentCatalogExecutionMode,
  AgentRole,
  AgentTool,
} from "@/api/generated/schema";
import type { AgentCatalog } from "@/api/types";
import {
  agentPayload,
  type AgentFormValues,
} from "@/features/agents/agent-form";

const catalog: AgentCatalog = {
  providers: [{ id: "local", name: "Local", models: ["model-a"] }],
  roles: [AgentRole.General],
  tools: [AgentTool.Read_files],
  execution: {
    mode: AgentCatalogExecutionMode.queue_only,
    ready: true,
    message: "Runs are accepted into the durable queue.",
  },
};

const values: AgentFormValues = {
  name: "  reviewer  ",
  description: "  Checks changes  ",
  role: AgentRole.General,
  provider: " local ",
  model: " model-a ",
  branch: " main ",
  tools: AgentTool.Read_files,
  instructions: "  Be concise.  ",
};

describe("agent form adapter", () => {
  it("maps typed form values to the API payload", () => {
    expect(agentPayload(catalog, values)).toEqual({
      name: "reviewer",
      description: "Checks changes",
      role: AgentRole.General,
      provider: "local",
      model: "model-a",
      branch: "main",
      tools: [AgentTool.Read_files],
      instructions: "Be concise.",
    });
  });

  it("rejects provider/model combinations outside the catalog", () => {
    expect(() =>
      agentPayload(catalog, { ...values, model: "unpublished" }),
    ).toThrow("Select a model supported by the selected provider.");
  });

  it("rejects tools outside the catalog", () => {
    expect(() =>
      agentPayload(catalog, { ...values, tools: "Execute shell" }),
    ).toThrow("Select tools published by the server catalog.");
  });
});
