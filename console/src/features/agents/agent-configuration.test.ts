import { describe, expect, it } from "vitest";
import type { AgentCatalog } from "@/api/types";
import {
  agentModelAfterProviderChange,
  agentModelOptions,
  defaultAgentModel,
  isValidAgentProviderModel,
} from "@/features/agents/agent-configuration";

const catalog: AgentCatalog = {
  providers: [
    { id: "local", name: "Local", models: ["model-a", "model-b"] },
    { id: "remote", name: "Remote", models: ["model-x"] },
  ],
  roles: [],
  tools: [],
  execution: {
    mode: "queue_only" as AgentCatalog["execution"]["mode"],
    ready: false,
    message: "Runs are accepted into the durable queue.",
  },
};

describe("Agent provider/model configuration", () => {
  it("only exposes models published by the selected provider", () => {
    expect(agentModelOptions(catalog, "local")).toEqual(["model-a", "model-b"]);
    expect(agentModelOptions(catalog, "remote")).toEqual(["model-x"]);
    expect(isValidAgentProviderModel(catalog, "local", "model-x")).toBe(false);
  });

  it("preserves a valid model and resets an incompatible one", () => {
    expect(agentModelAfterProviderChange(catalog, "local", "model-a")).toBe(
      "model-a",
    );
    expect(agentModelAfterProviderChange(catalog, "remote", "model-a")).toBe(
      "model-x",
    );
  });

  it("preselects a valid existing model or the first catalog model", () => {
    expect(defaultAgentModel(catalog, "local", "model-b")).toBe("model-b");
    expect(defaultAgentModel(catalog, "local", "missing-model")).toBe(
      "model-a",
    );
  });
});
