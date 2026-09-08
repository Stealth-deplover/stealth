"use client";

import { StatusBadge } from "@/components/ui/badge";
import {
  agentRunStatusLabel,
  type AgentRunStatus,
} from "@/features/agents/agent-run-state";

export function AgentRunStatusBadge({ status }: { status: AgentRunStatus }) {
  return <StatusBadge status={agentRunStatusLabel(status)} />;
}
