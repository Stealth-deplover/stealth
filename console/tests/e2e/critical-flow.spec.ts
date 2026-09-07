import { expect, test, type Page } from "@playwright/test";

const account = {
  id: "account-1",
  email: "developer@example.com",
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
  organization_id: "org-1",
  name: "production-api",
  created_at: "2026-01-01T00:00:00Z",
};
const pagination = { limit: 20, next_cursor: null };
const functionPageOne = {
  id: "function-1",
  project_id: project.id,
  name: "worker-page-one",
  runtime: "node-22",
  entrypoint: "src/index.main",
  commands: "",
  timeout_seconds: 15,
  enabled: true,
  logging: true,
  execute_permissions: [],
  status: "active",
  artifact_quota_bytes: 100000,
  artifact_used_bytes: 0,
  active_deployment_id: null,
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};
const functionPageTwo = {
  ...functionPageOne,
  id: "function-2",
  name: "worker-page-two",
};

async function gotoWithDevRetry(page: Page, url: string) {
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const response = await page.goto(url);
    if (response?.status() !== 404) return;
    await page.waitForTimeout(500);
  }
  throw new Error(`The development server continued returning 404 for ${url}.`);
}

async function installApiFixtures(page: Page) {
  await page.route("**/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const respond = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: "application/json",
        body: JSON.stringify(body),
      });

    if (path === "/v1/sessions/email-password" && request.method() === "POST")
      return route.fulfill({ status: 204 });
    if (path === "/v1/session" && request.method() === "DELETE")
      return route.fulfill({ status: 204 });
    if (path === "/v1/account") return respond({ account });
    if (path === "/v1/organizations")
      return respond({ organizations: [organization], pagination });
    if (path === "/v1/organizations/org-1/projects")
      return respond({ projects: [project], pagination });
    if (path === "/v1/projects/project-1") return respond({ project });
    if (path === "/v1/projects/project-1/usage")
      return respond({
        usage: {
          project_id: project.id,
          captured_at: "2026-01-01T00:00:00Z",
          application_users: 2,
          database_count: 1,
          database_table_count: 1,
          database_row_count: 12,
          storage_file_count: 3,
          storage_bytes: 1024,
          storage_quota_bytes: 100000,
          function_count: 0,
          function_artifact_bytes: 0,
          function_quota_bytes: 100000,
          site_count: 0,
          site_artifact_bytes: 0,
          site_reserved_bytes: 0,
          site_quota_bytes: 100000,
          realtime_event_count: 0,
          webhook_delivery_count_7d: 0,
          api_request_count_30d: 5,
          api_egress_bytes_30d: 0,
          function_invocation_count_30d: 0,
          function_failure_count_30d: 0,
          function_compute_ms_30d: 0,
        },
      });
    if (path === "/v1/projects/project-1/audit-events")
      return respond({ events: [], pagination });
    if (path === "/v1/projects/project-1/functions") {
      const cursor = new URL(request.url()).searchParams.get("cursor");
      return respond({
        functions: [
          cursor === "cursor-page-2" ? functionPageTwo : functionPageOne,
        ],
        pagination: {
          limit: 20,
          next_cursor: cursor === "cursor-page-2" ? null : "cursor-page-2",
        },
        can_manage: false,
      });
    }
    if (path === "/v1/projects/project-1/sites")
      return respond({ sites: [], pagination, can_manage: false });
    if (path === "/v1/projects/project-1/storage/buckets")
      return respond({ buckets: [], pagination, can_manage: false });
    if (path === "/v1/projects/project-1/databases")
      return respond({ databases: [], pagination, can_manage: false });
    return respond({});
  });
}

test("critical console flow can move from login to a resource and logout", async ({
  page,
}) => {
  await installApiFixtures(page);

  await page.goto("/login");
  await page.getByLabel("Email").fill(account.email);
  await page.getByLabel("Password").fill("correct horse battery staple");
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page).toHaveURL(/\/organizations$/);
  await expect(
    page.getByRole("heading", { name: "Organizations", exact: true, level: 1 }),
  ).toBeVisible();

  await page.getByRole("button", { name: /Open workspace/ }).click();
  await expect(page).toHaveURL(/\/organizations\/org-1\/projects$/);
  await page.getByRole("link", { name: /Open/ }).click();
  await expect(page).toHaveURL(/\/organizations\/org-1\/projects\/project-1$/);
  await expect(page.getByText("API connected")).toBeVisible();

  await page.getByRole("link", { name: "Functions", exact: true }).click();
  await expect(page).toHaveURL(/\/functions$/);
  await expect(
    page.getByRole("heading", { name: "Functions", exact: true, level: 1 }),
  ).toBeVisible();

  await page.getByRole("link", { name: "Logs", exact: true }).click();
  await expect(page).toHaveURL(/\/observability\/logs$/);
  await expect(
    page.getByRole("heading", { name: "Logs", exact: true, level: 1 }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Account menu" }).click();
  await page.getByRole("menuitem", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login$/);
  await expect(
    page.getByRole("heading", { name: "Sign in to Stealth" }),
  ).toBeVisible();
});

test("cursor pagination navigates next and previous server pages", async ({
  page,
}) => {
  await installApiFixtures(page);

  await gotoWithDevRetry(
    page,
    "/organizations/org-1/projects/project-1/functions",
  );
  await expect(page.getByText("worker-page-one")).toBeVisible();
  await expect(page.getByRole("button", { name: "First" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled();

  await page.getByRole("button", { name: "Next" }).click();
  await expect(page).toHaveURL(/\/functions\?cursor=cursor-page-2$/);
  await expect(page.getByText("worker-page-two")).toBeVisible();

  await page.getByRole("button", { name: "Previous" }).click();
  await expect(page).toHaveURL(/\/functions$/);
  await expect(page.getByText("worker-page-one")).toBeVisible();
});

test("cursor deep links expose First without inventing Previous", async ({
  page,
}) => {
  await installApiFixtures(page);

  await gotoWithDevRetry(
    page,
    "/organizations/org-1/projects/project-1/functions?status=active&cursor=cursor-page-2",
  );
  await expect(page.getByText("worker-page-two")).toBeVisible();
  await expect(page.getByRole("button", { name: "Previous" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "First" })).toBeEnabled();

  await page.getByRole("button", { name: "First" }).click();
  await expect(page).toHaveURL(/\/functions\?status=active$/);
  await expect(page.getByText("worker-page-one")).toBeVisible();
});
