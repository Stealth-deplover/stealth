import type { AgentRun } from "@/api/types";
import { formatDuration } from "@/lib/format";

export type AgentRunStatus = AgentRun["status"];
export type AgentRunTiming = Pick<AgentRun, "started_at" | "finished_at">;

const AGENT_RUN_STATUS_LABELS: Record<AgentRunStatus, string> = {
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  cancelled: "Cancelled",
};

export function agentRunStatusLabel(status: AgentRunStatus) {
  return AGENT_RUN_STATUS_LABELS[status];
}

export function isAgentRunActive(status: AgentRunStatus) {
  return status === "queued" || status === "running";
}

export function isAgentRunTerminal(status: AgentRunStatus) {
  return !isAgentRunActive(status);
}

function parseTimestamp(value: string | null | undefined) {
  if (!value) return null;
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? timestamp : null;
}

export function agentRunDurationMs(
  run: AgentRunTiming,
  now = new Date(),
): number | null {
  const startedAt = parseTimestamp(run.started_at);
  if (startedAt === null) return null;

  const finishedAt = parseTimestamp(run.finished_at) ?? now.valueOf();
  if (finishedAt < startedAt) return null;
  return finishedAt - startedAt;
}

export function formatAgentRunDuration(run: AgentRunTiming, now = new Date()) {
  return formatDuration(agentRunDurationMs(run, now));
}
