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
const agentId = "agent-1";

type RunStatus = "queued" | "running" | "completed" | "failed" | "cancelled";
type Scenario = "complete" | "fail" | "cancel";

const baseAgent = {
  id: agentId,
  project_id: project.id,
  project_name: project.name,
  name: "Repository agent",
  description: "Runs deterministic repository maintenance tasks.",
  role: "General",
  status: "idle",
  branch: "main",
  provider: "local",
  model: "model-a",
  current_task: null,
  last_active_at: null,
  tools: ["Read files"],
  instructions: null,
  created_by_account_id: account.id,
  created_at: "2026-01-02T00:00:00Z",
  updated_at: "2026-01-02T00:00:00Z",
};

function buildRun(
  status: RunStatus,
  prompt = "Fix failing tests in this repository.",
) {
  const started = status === "queued" ? null : "2026-01-02T00:00:02Z";
  const terminal =
    status === "completed" || status === "failed" || status === "cancelled";
  return {
    id: "run-1",
    agent_id: agentId,
    project_id: project.id,
    prompt,
    status,
    output_text:
      status === "completed" ? "All tests completed successfully." : null,
    error_message:
      status === "failed" ? "The test command returned failures." : null,
    steps:
      status === "queued"
        ? []
        : [
            {
              id: "step-1",
              type: "command",
              label: "Run tests",
              target: "npm run test",
              status: status === "completed" ? "done" : "pending",
            },
          ],
    changes: [],
    created_by_account_id: account.id,
    queued_at: "2026-01-02T00:00:00Z",
    started_at: started,
    finished_at: terminal ? "2026-01-02T00:00:10Z" : null,
    created_at: "2026-01-02T00:00:00Z",
    updated_at: "2026-01-02T00:00:10Z",
  };
}

async function installFixtures(
  page: Page,
  scenario: Scenario,
  withAgent = true,
) {
  let currentAgent = { ...baseAgent };
  const agents = withAgent ? [currentAgent] : [];
  let run: ReturnType<typeof buildRun> | null = null;
  let runDetailReads = 0;
  let cancelled = false;

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

    if (path === "/v1/agent-catalog")
      return respond({
        providers: [
          { id: "local", name: "Local gateway", models: ["model-a"] },
        ],
        roles: ["General", "Reviewer"],
        tools: ["Read files", "Search code"],
        execution: {
          mode: "queue_only",
          ready: false,
          message: "Runs are accepted into the durable queue.",
        },
      });

    if (path === "/v1/agents" && method === "GET")
      return respond({ agents, pagination });
    if (path === "/v1/agents" && method === "POST") {
      const body = request.postDataJSON() as {
        name: string;
        description?: string;
        role: string;
        provider: string;
        model: string;
        branch: string;
        tools?: string[];
        instructions?: string | null;
      };
      currentAgent = {
        ...baseAgent,
        id: "agent-created",
        name: body.name,
        description: body.description ?? "",
        role: body.role,
        provider: body.provider,
        model: body.model,
        branch: body.branch,
        tools: body.tools ?? [],
        instructions: body.instructions ?? null,
      };
      agents.push(currentAgent);
      return respond({ agent: currentAgent }, 201);
    }
    if (/^\/v1\/agents\/[^/]+$/.test(path) && method === "GET")
      return respond({ agent: currentAgent });

    if (path === `/v1/agents/${agentId}/runs` && method === "POST") {
      const body = request.postDataJSON() as { prompt: string };
      run = buildRun("queued", body.prompt);
      runDetailReads = 0;
      cancelled = false;
      return respond({ run }, 202);
    }
    if (path === `/v1/agents/${agentId}/runs` && method === "GET")
      return respond({ runs: run ? [run] : [], pagination });

    if (
      path === `/v1/agents/${agentId}/runs/${run?.id ?? "run-1"}/cancel` &&
      method === "POST"
    ) {
      cancelled = true;
      run = buildRun("cancelled", run?.prompt);
      return respond({ run });
    }
    if (path === `/v1/agents/${agentId}/runs/run-1` && method === "GET") {
      if (!run)
        return respond(
          { error: { code: "not_found", message: "run not found" } },
          404,
        );
      runDetailReads += 1;
      if (cancelled) {
        run = buildRun("cancelled", run.prompt);
      } else if (scenario === "cancel") {
        run = buildRun(runDetailReads >= 2 ? "running" : "queued", run.prompt);
      } else {
        const status =
          runDetailReads >= 3
            ? scenario === "fail"
              ? "failed"
              : "completed"
            : runDetailReads >= 2
              ? "running"
              : "queued";
        run = buildRun(status, run.prompt);
      }
      return respond({ run });
    }
    if (path === `/v1/agents/${agentId}/runs/run-1/logs` && method === "GET") {
      const logs =
        run && runDetailReads >= 2
          ? [
              {
                id: "log-1",
                run_id: "run-1",
                project_id: project.id,
                sequence: 1,
                level: "info",
                message: "worker: task execution started",
                created_at: "2026-01-02T00:00:03Z",
              },
            ]
          : [];
      return respond({ logs, pagination });
    }

    return respond({});
  });
}

test("creates an Agent from the server catalog and opens its detail", async ({
  page,
}) => {
  await installFixtures(page, "complete", false);
  await page.goto("/organizations/org-1/projects/project-1/agents");
  await expect(
    page.getByRole("heading", { name: "No agents yet" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Create agent" }).first().click();
  const dialog = page.getByRole("dialog");
  await dialog.getByLabel("Name", { exact: true }).fill("Test runner");
  await dialog
    .getByRole("button", { name: "Create agent", exact: true })
    .click();

  await expect(page).toHaveURL(/\/agents\/agent-created$/);
  await expect(
    page.getByRole("heading", { name: "Test runner" }),
  ).toBeVisible();
  await expect(page.getByText("Configuration", { exact: true })).toBeVisible();
});

test("runs a task from queued through running logs to completed result", async ({
  page,
}) => {
  await installFixtures(page, "complete");
  await page.goto(`/organizations/org-1/projects/project-1/agents/${agentId}`);
  await page
    .getByRole("link", { name: /Run agent/ })
    .first()
    .click();
  await page
    .getByLabel("Agent task prompt")
    .fill("Fix failing tests in this repository.");
  await page.getByRole("button", { name: "Run agent" }).first().click();

  await expect(page).toHaveURL(/\/runs\/run-1$/);
  await expect(page.getByText("Queued", { exact: true })).toBeVisible();
  await expect(
    page.getByText("This run is waiting for a worker."),
  ).toBeVisible();
  await expect(page.getByText("Running", { exact: true })).toBeVisible();
  await expect(page.getByText("worker: task execution started")).toBeVisible();
  await expect(page.getByText("Completed", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Result" })).toBeVisible();
  await expect(
    page.getByText("All tests completed successfully."),
  ).toBeVisible();
});

test("keeps failed run context and exposes its logs", async ({ page }) => {
  await installFixtures(page, "fail");
  await page.goto(
    `/organizations/org-1/projects/project-1/agents/${agentId}/runs`,
  );
  await page
    .getByLabel("Agent task prompt")
    .fill("Fix failing tests in this repository.");
  await page.getByRole("button", { name: "Run agent" }).first().click();

  await expect(page.getByText("Failed", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Run failed", { exact: true }).first(),
  ).toBeVisible();
  await expect(
    page.getByText("The test command returned failures."),
  ).toBeVisible();
  await expect(page.getByText("worker: task execution started")).toBeVisible();
});

test("cancels a running Agent Run and stops showing the cancel action", async ({
  page,
}) => {
  await installFixtures(page, "cancel");
  await page.goto(
    `/organizations/org-1/projects/project-1/agents/${agentId}/runs`,
  );
  await page
    .getByLabel("Agent task prompt")
    .fill("Inspect the repository state.");
  await page.getByRole("button", { name: "Run agent" }).first().click();

  await expect(page.getByText("Running", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel run" }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Cancel run", exact: true })
    .click();
  await expect(page.getByText("Cancelled", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Cancel run" })).toHaveCount(0);
});
