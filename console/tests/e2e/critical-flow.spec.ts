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
const functionDeploymentBase = {
  function_id: "function-1",
  project_id: project.id,
  version: 1,
  source: "upload",
  source_name: "function.zip",
  size_bytes: 128,
  checksum_sha256: "abc123",
  error_message: null,
  queued_at: "2026-01-02T00:00:00Z",
  build_started_at: null,
  built_at: null,
  activated_at: null,
  finished_at: null,
  created_at: "2026-01-02T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
};
const queuedFunctionDeployment = {
  ...functionDeploymentBase,
  id: "deployment-1",
  status: "ready",
  build_status: "queued",
};
const buildingFunctionDeployment = {
  ...queuedFunctionDeployment,
  build_status: "running",
  build_started_at: "2026-01-02T00:00:02Z",
  updated_at: "2026-01-02T00:00:02Z",
};
const failedFunctionDeployment = {
  ...buildingFunctionDeployment,
  build_status: "failed",
  error_message: "The build command exited with status 1.",
  updated_at: "2026-01-02T00:00:05Z",
};
const readyFunctionDeployment = {
  ...functionDeploymentBase,
  id: "deployment-2",
  version: 2,
  source_name: "function-fixed.zip",
  status: "ready",
  build_status: "succeeded",
  build_started_at: "2026-01-02T00:01:02Z",
  built_at: "2026-01-02T00:01:05Z",
  updated_at: "2026-01-02T00:01:05Z",
};
const functionBuildLog = {
  id: "build-log-1",
  deployment_id: "deployment-1",
  function_id: "function-1",
  project_id: project.id,
  sequence: 1,
  level: "error",
  message: "The build command exited with status 1.",
  created_at: "2026-01-02T00:00:05Z",
};
const acceptedExecution = {
  id: "execution-1",
  deployment_id: "deployment-2",
  function_id: "function-1",
  project_id: project.id,
  status: "accepted",
  trigger: "manual",
  input_json: { name: "Ada" },
  created_at: "2026-01-02T00:02:00Z",
  updated_at: "2026-01-02T00:02:00Z",
};
const onboardingOrganization = {
  id: "org-onboard",
  name: "Onboarding workspace",
  slug: "onboarding-workspace",
  created_at: "2026-01-02T00:00:00Z",
};
const onboardingProject = {
  id: "project-onboard",
  organization_id: onboardingOrganization.id,
  name: "first-project",
  created_at: "2026-01-02T00:00:00Z",
};
const onboardingDatabase = {
  id: "database-onboard",
  project_id: onboardingProject.id,
  name: "application-data",
  created_at: "2026-01-02T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
};
const emptyProjectUsage = {
  project_id: onboardingProject.id,
  captured_at: "2026-01-02T00:00:00Z",
  application_users: 0,
  database_count: 0,
  database_table_count: 0,
  database_row_count: 0,
  storage_file_count: 0,
  storage_bytes: 0,
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
  api_request_count_30d: 0,
  api_egress_bytes_30d: 0,
  function_invocation_count_30d: 0,
  function_failure_count_30d: 0,
  function_compute_ms_30d: 0,
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

async function installOnboardingFixtures(page: Page) {
  let organizationCreated = false;
  let projectCreated = false;
  let databaseCreated = false;

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
      return respond({
        organizations: organizationCreated ? [onboardingOrganization] : [],
        pagination,
      });
    if (path === "/v1/organizations" && method === "POST") {
      organizationCreated = true;
      return respond({ organization: onboardingOrganization }, 201);
    }
    if (path === "/v1/organizations/org-onboard/projects" && method === "GET")
      return respond({
        projects: projectCreated ? [onboardingProject] : [],
        pagination,
      });
    if (
      path === "/v1/organizations/org-onboard/projects" &&
      method === "POST"
    ) {
      projectCreated = true;
      return respond({ project: onboardingProject }, 201);
    }
    if (path === "/v1/projects/project-onboard" && method === "GET")
      return respond({ project: onboardingProject });
    if (path === "/v1/projects/project-onboard/usage")
      return respond({ usage: emptyProjectUsage });
    if (path === "/v1/projects/project-onboard/audit-events")
      return respond({ events: [], pagination });
    if (path === "/v1/projects/project-onboard/storage/buckets")
      return respond({ buckets: [], pagination, can_manage: true });
    if (path === "/v1/projects/project-onboard/databases" && method === "GET")
      return respond({
        databases: databaseCreated ? [onboardingDatabase] : [],
        pagination,
        can_manage: true,
      });
    if (
      path === "/v1/projects/project-onboard/databases" &&
      method === "POST"
    ) {
      databaseCreated = true;
      return respond({ database: onboardingDatabase }, 201);
    }
    if (
      path === "/v1/projects/project-onboard/databases/database-onboard" &&
      method === "GET"
    )
      return respond({ database: onboardingDatabase });
    if (
      path ===
        "/v1/projects/project-onboard/databases/database-onboard/tables" &&
      method === "GET"
    )
      return respond({ tables: [], pagination, can_manage: true });
    if (
      path ===
        "/v1/projects/project-onboard/databases/database-onboard/backups" &&
      method === "GET"
    )
      return respond({ backups: [], pagination, can_manage: true });
    return respond({});
  });
}

async function installDeploymentFixtures(page: Page) {
  let uploadCount = 0;
  let deploymentReads = 0;
  let executionReads = 0;

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
    if (path === "/v1/organizations")
      return respond({ organizations: [organization], pagination });
    if (path === "/v1/organizations/org-1/projects")
      return respond({ projects: [project], pagination });
    if (path === "/v1/projects/project-1") return respond({ project });
    if (
      path === "/v1/projects/project-1/functions/function-1" &&
      method === "GET"
    )
      return respond({ function: functionPageOne });
    if (
      path === "/v1/projects/project-1/functions/function-1/executions" &&
      method === "GET"
    )
      return respond({ executions: [acceptedExecution], pagination });
    if (
      path ===
      "/v1/projects/project-1/functions/function-1/executions/execution-1"
    ) {
      executionReads += 1;
      return respond({
        execution: {
          ...acceptedExecution,
          ...(executionReads > 1 && {
            status: "running",
            started_at: "2026-01-02T00:02:01Z",
          }),
          ...(executionReads > 2 && {
            status: "succeeded",
            finished_at: "2026-01-02T00:02:03Z",
            response_status: 200,
            output_json: { greeting: "Hello, Ada" },
          }),
        },
      });
    }
    if (
      path ===
      "/v1/projects/project-1/functions/function-1/executions/execution-1/logs"
    ) {
      const after = Number(
        new URL(request.url()).searchParams.get("after") ?? 0,
      );
      const logs = [
        { sequence: 1, message: "Runtime started" },
        { sequence: 2, message: "Runtime completed" },
      ].filter(
        (line) =>
          line.sequence > after && (line.sequence === 1 || executionReads > 2),
      );
      return respond({
        logs: logs.map((line) => ({
          ...line,
          level: "info",
          created_at: acceptedExecution.created_at,
        })),
        pagination,
      });
    }
    if (
      path === "/v1/projects/project-1/functions/function-1/deployments" &&
      method === "GET"
    ) {
      if (!uploadCount) return respond({ deployments: [], pagination });

      deploymentReads += 1;
      const deployment =
        uploadCount === 1
          ? deploymentReads === 1
            ? queuedFunctionDeployment
            : deploymentReads === 2
              ? buildingFunctionDeployment
              : failedFunctionDeployment
          : readyFunctionDeployment;
      return respond({
        deployments: [deployment],
        pagination,
        can_manage: true,
      });
    }
    if (
      path === "/v1/projects/project-1/functions/function-1/deployments" &&
      method === "POST"
    ) {
      uploadCount += 1;
      deploymentReads = 0;
      return respond(
        {
          deployment:
            uploadCount === 1
              ? queuedFunctionDeployment
              : readyFunctionDeployment,
        },
        201,
      );
    }
    if (
      path.match(
        /^\/v1\/projects\/project-1\/functions\/function-1\/deployments\/(deployment-1|deployment-2)$/,
      ) &&
      method === "GET"
    ) {
      const deployment = path.endsWith("/deployment-1")
        ? failedFunctionDeployment
        : readyFunctionDeployment;
      return respond({ deployment });
    }
    if (
      path ===
        "/v1/projects/project-1/functions/function-1/deployments/deployment-1/logs" &&
      method === "GET"
    )
      return respond({
        logs: new URL(request.url()).searchParams.has("after")
          ? []
          : [functionBuildLog],
        pagination,
      });
    if (path.endsWith("/deployments/deployment-2/logs"))
      return respond({ logs: [], pagination });
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

test("first-use flow creates an organization, project, and first database", async ({
  page,
}) => {
  await installOnboardingFixtures(page);

  await gotoWithDevRetry(page, "/organizations");
  await expect(
    page.getByRole("heading", { name: "Welcome to Stealth" }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Create organization" })
    .last()
    .click();
  await page.getByLabel("Display name").fill("Onboarding workspace");
  await page.getByLabel("Slug").fill("onboarding-workspace");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create organization" })
    .click();

  await expect(page).toHaveURL(/\/organizations\/org-onboard\/projects$/);
  await expect(
    page.getByRole("heading", { name: "No projects yet" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Create project" }).first().click();
  await page.getByLabel("Project name").fill("First project");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create project" })
    .click();

  await expect(page).toHaveURL(
    /\/organizations\/org-onboard\/projects\/project-onboard$/,
  );
  await expect(
    page.getByRole("heading", { name: "Your project is ready" }),
  ).toBeVisible();
  await page.getByRole("link", { name: /^Create database/ }).click();
  await expect(page).toHaveURL(/\/databases$/);
  await page.getByRole("button", { name: "Create database" }).first().click();
  await page.getByLabel("Name").fill("Application data");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create database" })
    .click();

  await expect(page).toHaveURL(
    /\/organizations\/org-onboard\/projects\/project-onboard\/databases\/database-onboard$/,
  );
  await expect(
    page.getByRole("heading", { name: "application-data" }),
  ).toBeVisible();
});

test("deployment flow exposes build failure, logs, and a truthful redeploy path", async ({
  page,
}) => {
  await installDeploymentFixtures(page);

  await gotoWithDevRetry(
    page,
    "/organizations/org-1/projects/project-1/functions/function-1",
  );
  await expect(
    page.getByRole("heading", { name: "worker-page-one" }),
  ).toBeVisible();
  await expect(page.getByText("Ready for first deployment")).toBeVisible();

  const archive = {
    name: "function.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("first archive"),
  };
  await page
    .locator('input[aria-label="Select function deployment archive"]')
    .setInputFiles(archive);
  await expect(page.getByText("Queued", { exact: true }).first()).toBeVisible();
  await expect(
    page.getByRole("tab", { name: "Deployments", exact: true }),
  ).toHaveAttribute("data-state", "active");
  await expect(
    page.getByRole("button", { name: "Activate", exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("Building", { exact: true }).first()).toBeVisible(
    {
      timeout: 10_000,
    },
  );
  await expect(page.getByText("Failed", { exact: true }).first()).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.getByRole("button", { name: "Activate", exact: true }),
  ).toHaveCount(0);

  await page.getByRole("link", { name: "Inspect" }).click();
  await expect(page).toHaveURL(/\/deployments\/deployment-1$/);
  await expect(page.getByRole("heading", { name: "Version 1" })).toBeVisible();
  await expect(page.getByText("Deployment failed")).toBeVisible();
  await expect(
    page.locator("#build-logs").getByText(functionBuildLog.message),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Deploy another archive" }),
  ).toBeVisible();

  await page.getByRole("link", { name: "Back to function" }).click();
  await expect(page).toHaveURL(/\/functions\/function-1$/);
  await page
    .locator('input[aria-label="Select function deployment archive"]')
    .setInputFiles({
      name: "function-fixed.zip",
      mimeType: "application/zip",
      buffer: Buffer.from("fixed archive"),
    });
  await expect(page.getByText("Ready", { exact: true }).first()).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Activate", exact: true }),
  ).toBeEnabled();

  await page.getByRole("link", { name: "Inspect" }).click();
  await expect(page).toHaveURL(/\/deployments\/deployment-2$/);
  await expect(
    page.getByRole("heading", { name: "Build logs", exact: true }),
  ).toBeVisible();
});

test("function execution detail follows runtime status, output, and incremental logs", async ({
  page,
}) => {
  await installDeploymentFixtures(page);
  await gotoWithDevRetry(
    page,
    "/organizations/org-1/projects/project-1/functions/function-1",
  );
  await page.getByRole("tab", { name: "Executions", exact: true }).click();
  await page.getByRole("link", { name: "execution-1" }).click();
  await expect(page).toHaveURL(/\/executions\/execution-1$/);
  await expect(
    page.getByRole("heading", { name: "Execution", exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Accepted", { exact: true })).toBeVisible();
  await expect(
    page.locator("pre").filter({ hasText: '"name": "Ada"' }),
  ).toBeVisible();
  await expect(
    page
      .locator("#execution-logs")
      .getByText("Runtime started", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Running", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByText("Succeeded", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(page.getByTitle("HTTP status 200")).toBeVisible();
  await expect(page.getByText("2.00 s", { exact: true })).toBeVisible();
  await expect(
    page.locator("pre").filter({ hasText: "Hello, Ada" }),
  ).toBeVisible();
  await expect(
    page
      .locator("#execution-logs")
      .getByText("Runtime completed", { exact: true }),
  ).toBeVisible();
  await expect(
    page
      .locator("#execution-logs")
      .getByText("Runtime started", { exact: true }),
  ).toHaveCount(1);
});

test("site deployment detail follows a deferred build through failure", async ({
  page,
}) => {
  await installApiFixtures(page);
  let deploymentReads = 0;
  const deployment = {
    ...functionDeploymentBase,
    id: "site-deployment-1",
    site_id: "site-1",
    status: "queued",
    build_status: "deferred",
    source_name: "site.zip",
    build_runtime: "node-22",
    build_command: "npm run build",
    output_directory: "dist",
    activate_requested: true,
    reserved_bytes: 128,
    archive_size_bytes: 128,
  };
  await page.route(
    "**/v1/projects/project-1/sites/site-1/deployments/**",
    async (route) => {
      const url = new URL(route.request().url());
      if (url.pathname.endsWith("/logs")) {
        return route.fulfill({
          json: {
            logs: url.searchParams.has("after")
              ? []
              : [
                  {
                    ...functionBuildLog,
                    message: "Site build failed: missing index.html",
                  },
                ],
            pagination,
          },
        });
      }
      deploymentReads += 1;
      return route.fulfill({
        json: {
          deployment: {
            ...deployment,
            ...(deploymentReads > 1 && {
              build_status: "running",
              build_started_at: "2026-01-02T00:00:02Z",
            }),
            ...(deploymentReads > 2 && {
              status: "failed",
              build_status: "failed",
              error_message: "The output directory has no index.html.",
              finished_at: "2026-01-02T00:00:05Z",
            }),
          },
        },
      });
    },
  );
  await gotoWithDevRetry(
    page,
    "/organizations/org-1/projects/project-1/sites/site-1/deployments/site-deployment-1",
  );
  await expect(page.getByRole("heading", { name: "Version 1" })).toBeVisible();
  await expect(page.getByText("Queued", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Build in progress", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("Building", { exact: true })).toBeVisible({
    timeout: 10_000,
  });
  await expect(
    page.getByText("Deployment failed", { exact: true }),
  ).toBeVisible({ timeout: 10_000 });
  await expect(
    page.getByText("Build in progress", { exact: true }),
  ).toHaveCount(0);
  await expect(page.getByText("3.00 s", { exact: true })).toBeVisible();
  await expect(
    page
      .locator("#build-logs")
      .getByText("Site build failed: missing index.html"),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Deploy another archive" }),
  ).toHaveAttribute(
    "href",
    "/organizations/org-1/projects/project-1/sites/site-1",
  );
});
