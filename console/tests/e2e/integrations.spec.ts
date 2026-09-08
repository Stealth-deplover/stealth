import { expect, test, type Page } from "@playwright/test";

const account = {
  id: "account-1",
  email: "owner@example.com",
  email_verified: true,
  created_at: "2026-01-01T00:00:00Z",
};
const organization = {
  id: "org-1",
  name: "Acme Inc",
  slug: "acme-inc",
  created_at: "2026-01-01T00:00:00Z",
};
const project = {
  id: "project-1",
  organization_id: organization.id,
  name: "production-api",
  created_at: "2026-01-01T00:00:00Z",
};
const pagination = { limit: 20, next_cursor: null };

type FixtureOptions = {
  canManage?: boolean;
  withWebhook?: boolean;
  withAPIKey?: boolean;
  apiKeyExpiresAt?: string | null;
};

async function installFixtures(page: Page, options: FixtureOptions = {}) {
  const canManage = options.canManage ?? true;
  const webhooks = options.withWebhook
    ? [
        {
          id: "webhook-1",
          project_id: project.id,
          name: "production-events",
          url: "https://example.com/hooks",
          events: ["function.execution.completed"],
          enabled: true,
          failure_count: 1,
          last_delivery_at: "2026-01-04T12:00:00Z",
          last_failure_at: "2026-01-04T12:00:00Z",
          created_at: "2026-01-02T00:00:00Z",
          updated_at: "2026-01-04T12:00:00Z",
        },
      ]
    : [];
  const deliveries = [
    {
      id: "delivery-1",
      webhook_id: "webhook-created",
      event_id: "event-1",
      event_name: "function.execution.completed",
      status: "failed",
      attempt_count: 2,
      last_status_code: 500,
      last_error: "upstream returned an error",
      delivered_at: null,
      created_at: "2026-01-04T12:00:00Z",
      updated_at: "2026-01-04T12:00:05Z",
    },
  ];
  const apiKeys = options.withAPIKey
    ? [
        {
          id: "key-1",
          project_id: project.id,
          name: "existing-key",
          prefix: "stl_key_existing",
          scopes: ["functions.read"],
          expires_at: options.apiKeyExpiresAt ?? null,
          revoked_at: null,
          last_used_at: "2026-01-03T00:00:00Z",
          created_at: "2026-01-02T00:00:00Z",
          updated_at: "2026-01-02T00:00:00Z",
        },
      ]
    : [];

  await page.route("**/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const respond = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });

    if (path === "/v1/account") return respond({ account });
    if (path === "/v1/organizations" && method === "GET")
      return respond({ organizations: [organization], pagination });
    if (path === "/v1/organizations/org-1/projects" && method === "GET")
      return respond({ projects: [project], pagination });
    if (path === "/v1/projects/project-1" && method === "GET")
      return respond({ project });

    if (path === "/v1/projects/project-1/webhooks") {
      if (method === "GET")
        return respond({ webhooks, pagination, can_manage: canManage });
      if (method === "POST") {
        const body = request.postDataJSON() as {
          name: string;
          url: string;
          events: string[];
          enabled: boolean;
        };
        const webhook = {
          id: "webhook-created",
          project_id: project.id,
          name: body.name,
          url: body.url,
          events: body.events,
          enabled: body.enabled,
          failure_count: 1,
          last_delivery_at: "2026-01-04T12:00:00Z",
          last_failure_at: "2026-01-04T12:00:00Z",
          created_at: "2026-01-04T00:00:00Z",
          updated_at: "2026-01-04T00:00:00Z",
        };
        webhooks.push(webhook);
        return respond({ webhook, secret: "whsec_test_secret" }, 201);
      }
    }
    if (path === "/v1/projects/project-1/webhooks/webhook-created") {
      if (method === "GET") return respond({ webhook: webhooks.at(-1) });
      if (method === "DELETE") {
        webhooks.splice(0, webhooks.length);
        return route.fulfill({ status: 204 });
      }
    }
    if (path === "/v1/projects/project-1/webhooks/webhook-created/deliveries") {
      if (method === "GET") return respond({ deliveries, pagination });
    }

    if (path === "/v1/projects/project-1/api-keys") {
      if (method === "GET")
        return respond({ keys: apiKeys, pagination, can_manage: canManage });
      if (method === "POST") {
        const body = request.postDataJSON() as {
          name: string;
          scopes: string[];
          expires_at: string | null;
        };
        const key = {
          id: "key-created",
          project_id: project.id,
          name: body.name,
          prefix: "stl_key_created",
          scopes: body.scopes,
          expires_at: body.expires_at,
          revoked_at: null,
          last_used_at: null,
          created_at: "2026-01-04T00:00:00Z",
          updated_at: "2026-01-04T00:00:00Z",
        };
        apiKeys.push(key);
        return respond({ key, secret: "stl_key_test_secret" }, 201);
      }
    }
    if (path === "/v1/projects/project-1/api-keys/key-created") {
      if (method === "GET") return respond({ key: apiKeys.at(-1) });
      if (method === "DELETE") {
        const key = apiKeys.at(-1);
        if (key) {
          key.revoked_at = "2026-01-04T01:00:00Z";
          key.updated_at = "2026-01-04T01:00:00Z";
        }
        return route.fulfill({ status: 204 });
      }
    }

    return respond({});
  });
}

test("creates a webhook, preserves its secret boundary, and inspects failed delivery metadata", async ({
  page,
}) => {
  await installFixtures(page);
  await page.goto("/organizations/org-1/projects/project-1/webhooks");
  await expect(
    page.getByRole("heading", { name: "No webhooks yet" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create webhook" }).first().click();
  const createDialog = page.getByRole("dialog");
  await createDialog.getByLabel("Name", { exact: true }).fill("events-hook");
  await createDialog
    .getByLabel("HTTPS URL", { exact: true })
    .fill("https://example.com/events");
  await createDialog
    .getByLabel("Events", { exact: true })
    .fill("function.execution.completed");
  await createDialog
    .getByRole("button", { name: "Create webhook", exact: true })
    .click();

  await expect(page.getByTestId("one-time-secret")).toContainText("whse");
  const browserStorage = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
  }));
  expect(JSON.stringify(browserStorage)).not.toContain("whsec_test_secret");
  await page.getByLabel("I have saved this secret safely").check();
  await page.getByRole("button", { name: "Done" }).click();

  await expect(page).toHaveURL(/\/webhooks\/webhook-created$/);
  await expect(
    page.getByRole("heading", { name: "events-hook" }),
  ).toBeVisible();
  await expect(
    page.getByRole("table").getByText("function.execution.completed"),
  ).toBeVisible();
  await expect(page.getByText("500", { exact: true })).toBeVisible();
  await expect(page.getByText("Failed", { exact: true })).toBeVisible();
  await expect(page.getByText("upstream returned an error")).toBeVisible();
  await expect(page.getByTestId("one-time-secret")).toHaveCount(0);
});

test("creates and revokes an API key without persisting its secret", async ({
  page,
}) => {
  await installFixtures(page);
  await page.goto("/organizations/org-1/projects/project-1/api-keys");
  await expect(
    page.getByRole("heading", { name: "No API keys yet" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create API key" }).first().click();
  const createDialog = page.getByRole("dialog");
  await createDialog.getByLabel("Name", { exact: true }).fill("ci-key");
  const permissionCheckboxes = createDialog.getByRole("checkbox");
  for (let index = 0; index < (await permissionCheckboxes.count()); index++) {
    await expect(permissionCheckboxes.nth(index)).not.toBeChecked();
  }
  await createDialog
    .getByRole("button", { name: "Create API key", exact: true })
    .click();
  await expect(
    page.getByText("Select at least one permission.", { exact: true }),
  ).toBeVisible();
  await createDialog
    .getByRole("checkbox", { name: "Functions · Write" })
    .check();
  await createDialog
    .getByRole("button", { name: "Create API key", exact: true })
    .click();

  await expect(page.getByTestId("one-time-secret")).toContainText("stl_");
  const browserStorage = await page.evaluate(() => ({
    local: { ...localStorage },
    session: { ...sessionStorage },
  }));
  expect(JSON.stringify(browserStorage)).not.toContain("stl_key_test_secret");
  await page.getByLabel("I have saved this secret safely").check();
  await page.getByRole("button", { name: "Done" }).click();

  await expect(page).toHaveURL(/\/api-keys\/key-created$/);
  await expect(page.getByRole("heading", { name: "ci-key" })).toBeVisible();
  await page.getByRole("button", { name: "Revoke key" }).click();
  const revokeDialog = page.getByRole("dialog");
  await revokeDialog
    .getByRole("button", { name: "Revoke key", exact: true })
    .click();
  await expect(page).toHaveURL(/\/api-keys$/);
  await expect(page.getByText("Revoked", { exact: true })).toBeVisible();
  await expect(page.getByTestId("one-time-secret")).toHaveCount(0);
});

test("shows expired API keys as expired instead of active", async ({
  page,
}) => {
  await installFixtures(page, {
    withAPIKey: true,
    apiKeyExpiresAt: "2000-01-01T00:00:00Z",
  });
  await page.goto("/organizations/org-1/projects/project-1/api-keys");

  await expect(page.getByText("Expired", { exact: true })).toBeVisible();
  await expect(page.getByText("Active", { exact: true })).toHaveCount(0);
});

test("hides integration mutations for read-only members", async ({ page }) => {
  await installFixtures(page, {
    canManage: false,
    withWebhook: true,
    withAPIKey: true,
  });

  await page.goto("/organizations/org-1/projects/project-1/webhooks");
  await expect(page.getByText("Read-only")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create webhook" }),
  ).toHaveCount(0);

  await page.goto("/organizations/org-1/projects/project-1/api-keys");
  await expect(page.getByText("Read-only")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create API key" }),
  ).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Revoke" })).toHaveCount(0);
});
