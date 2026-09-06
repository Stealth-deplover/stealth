import type { components } from "@/api/generated/schema";

export type Account = components["schemas"]["Account"];
export type Organization = components["schemas"]["Organization"];
export type Membership = components["schemas"]["Membership"];
export type OrganizationPlan = components["schemas"]["OrganizationPlan"];
export type Project = components["schemas"]["Project"];
export type AuditEvent = components["schemas"]["AuditEvent"];
export type HTTPTrace = components["schemas"]["HTTPTrace"];
export type ProjectUsage = components["schemas"]["ProjectUsage"];
export type ServiceLayoutItem = components["schemas"]["ProjectServiceLayout"];
export type StealthFunction = components["schemas"]["Function"];
export type FunctionDeployment = components["schemas"]["FunctionDeployment"];
export type FunctionExecution = components["schemas"]["FunctionExecution"];
export type FunctionBuildLog = components["schemas"]["FunctionBuildLog"];
export type FunctionExecutionLog = components["schemas"]["FunctionExecutionLog"];
export type Site = components["schemas"]["Site"];
export type SiteDeployment = components["schemas"]["SiteDeployment"];
export type SiteBuildLog = components["schemas"]["SiteBuildLog"];
export type Database = components["schemas"]["ProjectDatabase"];
export type DatabaseTable = components["schemas"]["DatabaseTable"];
export type DatabaseColumn = components["schemas"]["DatabaseColumn"];
export type DatabaseRow = components["schemas"]["DatabaseRow"];
export type StorageBucket = components["schemas"]["StorageBucket"];
export type StorageFile = components["schemas"]["StorageFile"];
export type ProjectUser = components["schemas"]["ProjectUser"];
export type APIKey = components["schemas"]["ProjectAPIKey"];
export type Webhook = components["schemas"]["Webhook"];
export type WebhookDelivery = components["schemas"]["WebhookDelivery"];
export type Agent = components["schemas"]["Agent"];
export type AgentRun = components["schemas"]["AgentRun"];
export type AgentRunLog = components["schemas"]["AgentRunLog"];
export type AgentCatalog = components["schemas"]["AgentCatalogResponse"];
export type AuthSettings = components["schemas"]["ProjectAuthSettings"];
export type Pagination = components["schemas"]["Pagination"];

export type Page<T> = { pagination?: Pagination } & Record<string, unknown> & { items: T[] };

export function pageItems<T>(page: unknown, key: string): T[] {
  if (!page || typeof page !== "object") return [];
  const value = (page as Record<string, unknown>)[key];
  return Array.isArray(value) ? (value as T[]) : [];
}
