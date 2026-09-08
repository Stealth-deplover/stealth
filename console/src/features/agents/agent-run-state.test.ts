import { describe, expect, it } from "vitest";
import { AgentRunStatus as AgentRunStatusEnum } from "@/api/generated/schema";
import {
  agentRunDurationMs,
  agentRunStatusLabel,
  formatAgentRunDuration,
  isAgentRunActive,
  isAgentRunTerminal,
} from "@/features/agents/agent-run-state";

const now = new Date("2026-01-01T00:05:00.000Z");

describe("Agent Run lifecycle", () => {
  it.each([
    [AgentRunStatusEnum.queued, true],
    [AgentRunStatusEnum.running, true],
    [AgentRunStatusEnum.completed, false],
    [AgentRunStatusEnum.failed, false],
    [AgentRunStatusEnum.cancelled, false],
  ] as const)(
    "marks %s as active only when it can still change",
    (status, active) => {
      expect(isAgentRunActive(status)).toBe(active);
      expect(isAgentRunTerminal(status)).toBe(!active);
    },
  );

  it("keeps backend status labels explicit", () => {
    expect(agentRunStatusLabel(AgentRunStatusEnum.queued)).toBe("Queued");
    expect(agentRunStatusLabel(AgentRunStatusEnum.running)).toBe("Running");
    expect(agentRunStatusLabel(AgentRunStatusEnum.completed)).toBe("Completed");
    expect(agentRunStatusLabel(AgentRunStatusEnum.failed)).toBe("Failed");
    expect(agentRunStatusLabel(AgentRunStatusEnum.cancelled)).toBe("Cancelled");
  });

  it("calculates terminal duration from backend timestamps", () => {
    expect(
      agentRunDurationMs(
        {
          started_at: "2026-01-01T00:00:02.000Z",
          finished_at: "2026-01-01T00:01:44.000Z",
        },
        now,
      ),
    ).toBe(102_000);
    expect(
      formatAgentRunDuration(
        {
          started_at: "2026-01-01T00:00:02.000Z",
          finished_at: "2026-01-01T00:01:44.000Z",
        },
        now,
      ),
    ).toBe("102.00 s");
  });

  it("uses a fixed current time for a running duration", () => {
    expect(
      agentRunDurationMs(
        {
          started_at: "2026-01-01T00:04:12.500Z",
          finished_at: null,
        },
        now,
      ),
    ).toBe(47_500);
  });

  it("does not report duration before a worker has started", () => {
    expect(
      agentRunDurationMs({ started_at: null, finished_at: null }, now),
    ).toBeNull();
  });
});
