import type { paths } from "@/api/generated/schema";

export type CursorQuery = NonNullable<paths["/v1/organizations"]["get"]["parameters"]["query"]>;
export type AgentListQuery = NonNullable<paths["/v1/agents"]["get"]["parameters"]["query"]>;

/** The API default is 20; keeping the console at that size avoids hidden first-page overfetching. */
export const DEFAULT_PAGE_SIZE = 20;

export function withCursorPage(query?: CursorQuery): CursorQuery {
  return { limit: DEFAULT_PAGE_SIZE, ...query };
}

export function withAgentPage(query?: AgentListQuery): AgentListQuery {
  return { limit: DEFAULT_PAGE_SIZE, ...query };
}

export function nextCursor(page: { pagination?: { next_cursor?: string | null } } | undefined): string | null {
  return page?.pagination?.next_cursor ?? null;
}
