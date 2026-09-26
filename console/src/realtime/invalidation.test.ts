import { describe, expect, it } from "vitest";
import {
  adminRealtimeInvalidationKeys,
  eventTypesForProjectStream,
  realtimeInvalidationKeys,
  type RealtimeNotification,
} from "@/realtime/invalidation";

describe("realtime query invalidation", () => {
  it("maps admin notifications to bounded control-room queries", () => {
    expect(
      adminRealtimeInvalidationKeys({
        type: "admin.alert.updated",
        resource_id: "alert-1",
      }),
    ).toEqual([
      ["admin", "audit-events"],
      ["admin", "alerts"],
      ["admin", "alert-events"],
      ["admin", "alert", "alert-1"],
    ]);
  });

  it("invalidates an Agent run list and detail without treating the event as state", () => {
    const event: RealtimeNotification = {
      type: "agent.run.running",
      resource_id: "run-1",
      payload: { agent_id: "agent-1", status: "running" },
    };
    expect(realtimeInvalidationKeys("project-1", event)).toEqual([
      ["agent-runs", "agent-1"],
      ["agent-run", "agent-1", "run-1"],
      ["agents", "project-1"],
      ["agent", "agent-1"],
    ]);
  });

  it("invalidates App queries from resource events without using event payload as state", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "app.update",
        resource_id: "app-1",
        payload: { runtime_status: "running", workload: { port: 9000 } },
      }),
    ).toEqual([
      ["apps", "project-1"],
      ["app", "project-1", "app-1"],
    ]);
  });

  it("invalidates the App detail when its runtime observation changes", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "app.runtime.updated",
        resource_id: "app-1",
        payload: { runtime_status: "running", observed_generation: 2 },
      }),
    ).toEqual([
      ["apps", "project-1"],
      ["app", "project-1", "app-1"],
    ]);
  });

  it("refetches App state and variable metadata after an environment change", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "app.environment_variable.updated",
        resource_id: "app-1",
        payload: { key: "PAYMENT_TOKEN", has_value: false },
      }),
    ).toEqual([
      ["app", "project-1", "app-1"],
      ["app-environment-variables", "project-1", "app-1"],
    ]);
  });

  it("maps database row and storage file notifications to scoped caches", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "database_row.update",
        resource_id: "row-1",
        payload: { database_id: "database-1", table_id: "table-1" },
      }),
    ).toEqual([
      ["rows", "project-1", "database-1", "table-1"],
      ["row", "project-1", "database-1", "table-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "agent.run.queued",
        resource_id: "run-1",
        payload: { agent_id: "agent-1", status: "queued" },
      }),
    ).toEqual([
      ["agent-runs", "agent-1"],
      ["agent-run", "agent-1", "run-1"],
      ["agents", "project-1"],
      ["agent", "agent-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "storage_file.create",
        resource_id: "file-1",
        payload: { bucket_id: "bucket-1" },
      }),
    ).toEqual([
      ["files", "project-1", "bucket-1"],
      ["file", "project-1", "bucket-1", "file-1"],
      ["bucket", "project-1", "bucket-1"],
      ["storage", "project-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "storage_file.update",
        resource_id: "file-1",
        payload: { bucket_id: "bucket-1" },
      }),
    ).toEqual([
      ["files", "project-1", "bucket-1"],
      ["file", "project-1", "bucket-1", "file-1"],
      ["file", "project-1", "bucket-1"],
    ]);
  });

  it("invalidates bucket detail separately from file changes", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "storage_bucket.update",
        resource_id: "bucket-1",
      }),
    ).toEqual([
      ["storage", "project-1"],
      ["bucket", "project-1", "bucket-1"],
    ]);
  });

  it("maps messaging and webhook secret changes to scoped caches", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "messaging.subscriber.create",
        resource_id: "subscriber-1",
        payload: { topic_id: "topic-1" },
      }),
    ).toEqual([
      ["messaging-providers", "project-1"],
      ["messaging-topics", "project-1"],
      ["messaging-messages", "project-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "webhook.secret_rotate",
        resource_id: "webhook-1",
      }),
    ).toEqual([
      ["webhooks", "project-1"],
      ["webhook", "project-1", "webhook-1"],
    ]);
  });

  it("maps schema, credential metadata, and service events to their scopes", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "database_column.create",
        resource_id: "column-1",
        payload: { database_id: "database-1", table_id: "table-1" },
      }),
    ).toEqual([
      ["columns", "project-1", "database-1", "table-1"],
      ["rows", "project-1", "database-1", "table-1"],
      ["row", "project-1", "database-1", "table-1"],
      ["indexes", "project-1", "database-1", "table-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "database_table.update",
        resource_id: "table-1",
        payload: { database_id: "database-1" },
      }),
    ).toEqual([
      ["tables", "project-1", "database-1"],
      ["table", "project-1", "database-1", "table-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "function_variable.update",
        resource_id: "variable-1",
        payload: { function_id: "function-1" },
      }),
    ).toEqual([["function-variables", "project-1", "function-1"]]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "project_api_key.revoke",
        resource_id: "key-1",
      }),
    ).toEqual([
      ["api-keys", "project-1"],
      ["api-key", "project-1", "key-1"],
    ]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "site_domain.verify",
        resource_id: "domain-1",
        payload: { site_id: "site-1" },
      }),
    ).toEqual([
      ["sites", "project-1"],
      ["site", "project-1", "site-1"],
    ]);
  });

  it("ignores unknown events and keeps unrelated event metadata out of keys", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "future.resource.changed",
        resource_id: "other-project-resource",
      }),
    ).toEqual([]);
    expect(
      realtimeInvalidationKeys("project-1", {
        type: "database_future.changed",
        resource_id: "other-project-resource",
      }),
    ).toEqual([]);
  });

  it("subscribes to the events that the backend fans out for rows and files", () => {
    const events = eventTypesForProjectStream();
    expect(events).toEqual(
      expect.arrayContaining([
        "database_row.create",
        "database_row.update",
        "database_row.delete",
        "storage_file.create",
        "storage_file.update",
        "storage_file.delete",
        "messaging.subscriber.create",
        "messaging.subscriber.delete",
        "messaging.message.create",
        "messaging.message.cancel",
        "webhook.secret_rotate",
        "site_domain.create",
        "site_domain.delete",
        "site_domain.verify",
        "app.create",
        "app.update",
        "app.delete",
        "app.environment_variable.updated",
      ]),
    );
  });

  it("invalidates deployment queries using the resource metadata", () => {
    expect(
      realtimeInvalidationKeys("project-1", {
        event: "function_deployment.updated",
        resource_id: "deployment-1",
        payload: { function_id: "function-1" },
      }),
    ).toEqual([
      ["function", "project-1", "function-1"],
      ["function-deployments", "project-1", "function-1"],
      ["function-deployment", "project-1", "function-1", "deployment-1"],
    ]);
  });
});
