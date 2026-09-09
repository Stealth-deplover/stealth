import { expect, test, type Page } from "@playwright/test";

const base = "/organizations/org-1/projects/project-1";
const apiBase = "/v1/projects/project-1";
const pagination = { limit: 20, next_cursor: null };
const timestamps = {
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};
const database = {
  id: "database-1",
  project_id: "project-1",
  name: "application-data",
  ...timestamps,
};
const table = {
  id: "table-1",
  project_id: "project-1",
  database_id: database.id,
  name: "contacts",
  row_security: true,
  create_permissions: [],
  read_permissions: [],
  update_permissions: [],
  delete_permissions: [],
  ...timestamps,
};
const bucket = {
  id: "bucket-1",
  project_id: "project-1",
  name: "app-assets",
  file_security: true,
  create_permissions: [],
  read_permissions: [],
  update_permissions: [],
  delete_permissions: [],
  used_bytes: 0,
  quota_bytes: 1000000,
  max_file_size_bytes: 100000,
  ...timestamps,
};
const object = {
  id: "file-1",
  bucket_id: bucket.id,
  project_id: "project-1",
  name: "avatar.txt",
  mime_type: "text/plain",
  size_bytes: 12,
  checksum_sha256: "a".repeat(64),
  read_permissions: [],
  update_permissions: [],
  delete_permissions: [],
  ...timestamps,
};

async function workspace(page: Page) {
  await page.route("**/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === "/v1/account")
      return route.fulfill({
        json: {
          account: {
            id: "account-1",
            email: "dev@example.com",
            email_verified: true,
            ...timestamps,
          },
        },
      });
    if (path === "/v1/organizations")
      return route.fulfill({
        json: {
          organizations: [
            { id: "org-1", name: "Acme", slug: "acme", ...timestamps },
          ],
          pagination,
        },
      });
    if (path === "/v1/organizations/org-1/projects")
      return route.fulfill({
        json: {
          projects: [
            {
              id: "project-1",
              organization_id: "org-1",
              name: "production-api",
              ...timestamps,
            },
          ],
          pagination,
        },
      });
    if (path === apiBase)
      return route.fulfill({
        json: {
          project: {
            id: "project-1",
            organization_id: "org-1",
            name: "production-api",
            ...timestamps,
          },
        },
      });
    return route.fulfill({ json: {} });
  });
}

test("database creates a table, schema, typed rows, and a restorable backup", async ({
  page,
}) => {
  await workspace(page);
  let hasDatabase = false;
  let hasTable = false;
  let hasColumn = false;
  let rowData: Record<string, unknown> | undefined;
  let snapshot: Record<string, unknown> | undefined;
  let backupCreated = false;
  let revision = 0;
  const row = () => ({
    id: "row-1",
    table_id: table.id,
    project_id: "project-1",
    data: rowData,
    read_permissions: [],
    update_permissions: [],
    delete_permissions: [],
    ...timestamps,
    updated_at: `2026-01-01T00:00:0${revision}Z`,
  });
  const backup = {
    id: "backup-1",
    project_id: "project-1",
    database_id: database.id,
    size_bytes: 1024,
    checksum_sha256: "b".repeat(64),
    created_at: timestamps.created_at,
  };
  await page.route("**/v1/projects/project-1/databases**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    if (path === `${apiBase}/databases`) {
      if (method === "POST") {
        expect(request.postDataJSON()).toEqual({ name: database.name });
        hasDatabase = true;
        return route.fulfill({ status: 201, json: { database } });
      }
      return route.fulfill({
        json: {
          databases: hasDatabase ? [database] : [],
          pagination,
          can_manage: true,
        },
      });
    }
    if (path.endsWith("/databases/database-1"))
      return route.fulfill({ json: { database } });
    if (path.endsWith("/tables")) {
      if (method === "POST") {
        expect(request.postDataJSON()).toEqual({
          name: "contacts",
          row_security: true,
        });
        hasTable = true;
        return route.fulfill({ status: 201, json: { table } });
      }
      return route.fulfill({
        json: { tables: hasTable ? [table] : [], pagination, can_manage: true },
      });
    }
    if (path.endsWith("/tables/table-1"))
      return route.fulfill({ json: { table } });
    if (path.endsWith("/columns")) {
      const column = {
        id: "column-1",
        table_id: table.id,
        key: "email",
        type: "text",
        required: true,
        ...timestamps,
      };
      if (method === "POST") {
        expect(request.postDataJSON()).toEqual({
          key: "email",
          type: "text",
          required: true,
        });
        hasColumn = true;
        return route.fulfill({ status: 201, json: { column } });
      }
      return route.fulfill({
        json: { columns: hasColumn ? [column] : [], pagination },
      });
    }
    if (path.endsWith("/indexes"))
      return route.fulfill({ json: { indexes: [], pagination } });
    if (path.endsWith("/rows")) {
      if (method === "POST") {
        rowData = request.postDataJSON().data;
        return route.fulfill({ status: 201, json: { row: row() } });
      }
      return route.fulfill({
        json: { rows: rowData ? [row()] : [], pagination },
      });
    }
    if (path.endsWith("/rows/row-1")) {
      if (method === "PATCH") {
        rowData = { ...rowData, ...request.postDataJSON().data };
        revision++;
      }
      if (method === "DELETE") {
        rowData = undefined;
        return route.fulfill({ status: 204 });
      }
      return route.fulfill({ json: { row: row() } });
    }
    if (path.endsWith("/backups")) {
      if (method === "POST") {
        expect(new URL(request.url()).searchParams.get("max_rows")).toBe(
          "10000",
        );
        backupCreated = true;
        snapshot = { ...rowData };
        return route.fulfill({ status: 201, json: { backup } });
      }
      return route.fulfill({
        json: {
          backups: backupCreated ? [backup] : [],
          pagination,
          can_manage: true,
        },
      });
    }
    if (path.endsWith("/backups/backup-1/restore")) {
      rowData = { ...snapshot };
      revision++;
      return route.fulfill({
        json: {
          backup_id: backup.id,
          result: {
            tables: 1,
            columns: 1,
            rows: 1,
            indexes: 0,
            relationships: 0,
          },
        },
      });
    }
    return route.fulfill({
      status: 404,
      json: { error: { message: "Fixture route not found" } },
    });
  });
  await page.goto(`${base}/databases`);
  await page
    .getByRole("button", { name: "Create database", exact: true })
    .first()
    .click();
  await page
    .getByRole("dialog")
    .getByLabel("Name", { exact: true })
    .fill(database.name);
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create database", exact: true })
    .click();
  await expect(page).toHaveURL(/\/databases\/database-1$/);
  await expect(page.getByText("No tables yet", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Create table", exact: true })
    .first()
    .click();
  await page.getByLabel("Table name", { exact: true }).fill("contacts");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create table", exact: true })
    .click();
  await expect(page).toHaveURL(/\/tables\/table-1$/);
  await expect(page.getByText("No rows yet", { exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "Schema", exact: true }).click();
  await page
    .getByRole("button", { name: "Create column", exact: true })
    .click();
  await page.getByLabel("Column key", { exact: true }).fill("email");
  await page.getByLabel("Required", { exact: true }).selectOption("true");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create column", exact: true })
    .click();
  await expect(
    page.getByRole("cell", { name: "email", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "No", exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Rows", exact: true }).click();
  await page
    .getByRole("button", { name: "Add row", exact: true })
    .first()
    .click();
  await page.getByLabel("Row data (JSON)").fill('{"email":"ada@example.com"}');
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Add row", exact: true })
    .click();
  await expect(page.getByRole("dialog", { name: "Row detail" })).toContainText(
    "ada@example.com",
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close dialog" })
    .click();
  await page
    .getByRole("link", { name: "Back to database", exact: true })
    .click();
  await page.getByRole("tab", { name: "Backups", exact: true }).click();
  await expect(page.getByText("No backups yet", { exact: true })).toBeVisible();
  await page
    .getByRole("button", { name: "Create backup", exact: true })
    .click();
  await expect(
    page.getByRole("cell", { name: "1.0 KB", exact: true }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Tables", exact: true }).click();
  await page.getByRole("link", { name: "contacts", exact: true }).click();
  await page.getByRole("button", { name: /Inspect row-1/ }).click();
  await page.getByRole("button", { name: "Edit row", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Edit row", exact: true })
    .getByLabel("Row data (JSON)")
    .fill('{"email":"changed@example.com"}');
  await page.getByRole("button", { name: "Save row", exact: true }).click();
  await expect(page.getByRole("dialog", { name: "Row detail" })).toContainText(
    "changed@example.com",
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close dialog" })
    .click();
  await page
    .getByRole("link", { name: "Back to database", exact: true })
    .click();
  await page.getByRole("tab", { name: "Backups", exact: true }).click();
  await page.getByRole("button", { name: "Restore", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "replaces all current tables",
  );
  await page
    .getByRole("button", { name: "Restore backup", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("tab", { name: "Tables", exact: true }).click();
  await page.getByRole("link", { name: "contacts", exact: true }).click();
  await page.getByRole("button", { name: /Inspect row-1/ }).click();
  await expect(page.getByRole("dialog", { name: "Row detail" })).toContainText(
    "ada@example.com",
  );
  await page.getByRole("button", { name: "Delete row", exact: true }).click();
  await page
    .getByRole("dialog", { name: "Delete this row?" })
    .getByRole("button", { name: "Delete row", exact: true })
    .click();
  await expect(page.getByText("No rows yet", { exact: true })).toBeVisible();
});

test("database backup mutations stay hidden for read-only members", async ({
  page,
}) => {
  await workspace(page);
  await page.route("**/v1/projects/project-1/databases/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/database-1"))
      return route.fulfill({ json: { database } });
    if (path.endsWith("/tables"))
      return route.fulfill({
        json: { tables: [], pagination, can_manage: false },
      });
    if (path.endsWith("/backups"))
      return route.fulfill({
        json: { backups: [], pagination, can_manage: false },
      });
    return route.fulfill({ json: {} });
  });

  await page.goto(`${base}/databases/database-1`);
  await page.getByRole("tab", { name: "Backups", exact: true }).click();
  await expect(page.getByText("No backups yet", { exact: true })).toBeVisible();
  await expect(
    page
      .getByRole("tabpanel", { name: "Backups" })
      .getByText("Read-only", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create backup", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Restore", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Delete", exact: true }),
  ).toHaveCount(0);
});

test("storage creates a bucket, retries upload, inspects, renames, downloads, and deletes an object", async ({
  page,
}) => {
  await workspace(page);
  let hasBucket = false;
  let hasObject = false;
  let uploadAttempts = 0;
  let name = object.name;
  await page.route("**/v1/projects/project-1/storage/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const file = { ...object, name };
    if (path.endsWith("/buckets")) {
      if (method === "POST") {
        expect(request.postDataJSON()).toEqual({
          name: bucket.name,
          file_security: true,
        });
        hasBucket = true;
        return route.fulfill({ status: 201, json: { bucket } });
      }
      return route.fulfill({
        json: {
          buckets: hasBucket ? [bucket] : [],
          pagination,
          can_manage: true,
        },
      });
    }
    if (path.endsWith("/buckets/bucket-1"))
      return route.fulfill({
        json: { bucket: { ...bucket, used_bytes: hasObject ? 12 : 0 } },
      });
    if (path.endsWith("/files")) {
      if (method === "POST") {
        expect(request.headers()["content-type"]).toContain(
          "multipart/form-data",
        );
        expect(request.postData()).toContain('filename="avatar.txt"');
        uploadAttempts++;
        if (uploadAttempts === 1)
          return route.fulfill({
            status: 500,
            json: { error: { message: "Storage temporarily unavailable" } },
          });
        hasObject = true;
        return route.fulfill({ status: 201, json: { file } });
      }
      return route.fulfill({
        json: { files: hasObject ? [file] : [], pagination, can_manage: true },
      });
    }
    if (path.endsWith("/download"))
      return route.fulfill({
        headers: {
          "Content-Disposition": `attachment; filename="${name}"`,
          "Content-Type": "text/plain",
        },
        body: "hello world!",
      });
    if (path.endsWith("/files/file-1")) {
      if (method === "DELETE") {
        hasObject = false;
        return route.fulfill({ status: 204 });
      }
      if (method === "PATCH") {
        name = request.postDataJSON().name;
        return route.fulfill({
          json: {
            file: { ...object, name, updated_at: "2026-01-01T00:01:00Z" },
          },
        });
      }
      return route.fulfill({ json: { file: { ...object, name } } });
    }
    return route.fulfill({ status: 404 });
  });
  await page.goto(`${base}/storage`);
  await expect(
    page.getByText("No storage buckets yet", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Create bucket", exact: true })
    .first()
    .click();
  await page
    .getByRole("dialog")
    .getByLabel("Name", { exact: true })
    .fill(bucket.name);
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Create bucket", exact: true })
    .click();
  await expect(page).toHaveURL(/\/storage\/bucket-1$/);
  await expect(
    page.getByText("This bucket is empty", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Upload object", exact: true })
    .first()
    .click();
  await page.getByLabel("Choose file", { exact: true }).setInputFiles({
    name: object.name,
    mimeType: object.mime_type,
    buffer: Buffer.from("hello world!"),
  });
  await expect(page.getByRole("dialog")).toContainText("avatar.txt");
  await expect(page.getByRole("dialog")).toContainText("12 B");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Upload object", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "Could not upload object",
  );
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Upload object", exact: true })
    .click();
  await expect(
    page.getByRole("dialog", { name: "Object detail" }),
  ).toContainText(object.checksum_sha256);
  await page
    .getByRole("button", { name: "Rename object", exact: true })
    .click();
  await page.getByLabel("Object name", { exact: true }).fill("renamed.txt");
  await page.getByRole("button", { name: "Save name", exact: true }).click();
  await expect(
    page.getByRole("dialog", { name: "Object detail" }),
  ).toContainText("renamed.txt");
  const downloadEvent = page.waitForEvent("download");
  await page
    .getByRole("link", { name: "Download object", exact: true })
    .click();
  expect((await downloadEvent).suggestedFilename()).toBe("renamed.txt");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close dialog" })
    .click();
  await expect(
    page.getByRole("button", { name: "renamed.txt", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Delete renamed.txt", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText('Delete "renamed.txt"?');
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "renamed.txt", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Delete renamed.txt", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Delete object", exact: true })
    .click();
  await expect(
    page.getByText("This bucket is empty", { exact: true }),
  ).toBeVisible();
});

test("database rows retain server filters, sorting, and deep-link cursor controls", async ({
  page,
}) => {
  await workspace(page);
  const requested: URL[] = [];
  await page.route("**/v1/projects/project-1/databases/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname.endsWith("/tables/table-1"))
      return route.fulfill({ json: { table } });
    if (url.pathname.endsWith("/tables"))
      return route.fulfill({
        json: { tables: [table], pagination, can_manage: true },
      });
    if (url.pathname.endsWith("/columns"))
      return route.fulfill({
        json: {
          columns: [
            {
              id: "column-1",
              table_id: table.id,
              key: "email",
              type: "text",
              required: true,
              ...timestamps,
            },
          ],
          pagination,
        },
      });
    if (url.pathname.endsWith("/indexes"))
      return route.fulfill({
        json: {
          indexes: [
            {
              id: "index-1",
              table_id: table.id,
              name: "by_email",
              type: "key",
              column_keys: ["email"],
              directions: ["asc"],
              ...timestamps,
            },
          ],
          pagination,
        },
      });
    if (url.pathname.endsWith("/rows")) {
      requested.push(url);
      const cursor = url.searchParams.get("cursor");
      return route.fulfill({
        json: {
          rows: [],
          pagination: { limit: 50, next_cursor: cursor ? cursor : "next-page" },
        },
      });
    }
    return route.fulfill({ json: { database } });
  });
  await page.goto(
    `${base}/databases/database-1/tables/table-1?rows_cursor=deep-page&keep=value`,
  );
  await expect(
    page.getByRole("button", { name: "First", exact: true }),
  ).toBeEnabled();
  await expect(
    page.getByRole("button", { name: "Previous", exact: true }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeDisabled();
  await page
    .getByLabel("Equality filters (JSON)", { exact: true })
    .fill('{"email":"ada@example.com"}');
  await page.getByLabel(/^Order by/).selectOption("email");
  await page.getByLabel(/^Direction/).selectOption("desc");
  await page.getByRole("button", { name: "Apply", exact: true }).click();
  await expect
    .poll(() => requested.at(-1)?.searchParams.get("filter"))
    .toBe('{"email":"ada@example.com"}');
  expect(requested.at(-1)?.searchParams.get("order_by")).toBe("email");
  expect(requested.at(-1)?.searchParams.get("order_direction")).toBe("desc");
  expect(requested.at(-1)?.searchParams.has("cursor")).toBe(false);
  expect(new URL(page.url()).searchParams.get("keep")).toBe("value");
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await expect
    .poll(() => requested.at(-1)?.searchParams.get("cursor"))
    .toBe("next-page");
  expect(requested.at(-1)?.searchParams.get("filter")).toBe(
    '{"email":"ada@example.com"}',
  );
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Previous", exact: true }).click();
  await expect
    .poll(() => new URL(page.url()).searchParams.has("rows_cursor"))
    .toBe(false);
  expect(new URL(page.url()).searchParams.get("filter")).toBe(
    '{"email":"ada@example.com"}',
  );
  await expect(
    page.getByRole("button", { name: "Next", exact: true }),
  ).toBeEnabled();
});

test("active Function deployment never offers Activate despite stale row status", async ({
  page,
}) => {
  await workspace(page);
  await page.route("**/v1/projects/project-1/functions/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path.endsWith("/deployments"))
      return route.fulfill({
        json: {
          deployments: [
            {
              id: "deployment-1",
              version: 1,
              status: "ready",
              build_status: "succeeded",
              source: "upload",
              ...timestamps,
            },
          ],
          pagination,
          can_manage: true,
        },
      });
    if (path.endsWith("/executions"))
      return route.fulfill({ json: { executions: [], pagination } });
    return route.fulfill({
      json: {
        function: {
          id: "function-1",
          name: "worker",
          status: "active",
          runtime: "node-22",
          active_deployment_id: "deployment-1",
          ...timestamps,
        },
      },
    });
  });
  await page.goto(`${base}/functions/function-1`);
  await page.getByRole("tab", { name: "Deployments", exact: true }).click();
  await expect(
    page.getByRole("table").getByText("Active", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Activate", exact: true }),
  ).toHaveCount(0);
});

test("site upload opens the returned deployment", async ({ page }) => {
  await workspace(page);
  await page.route("**/v1/projects/project-1/sites/**", async (route) => {
    const path = new URL(route.request().url()).pathname;
    const deployment = {
      id: "deployment-1",
      site_id: "site-1",
      version: 1,
      source: "upload",
      source_name: "site.zip",
      size_bytes: 10,
      status: "active",
      build_status: "succeeded",
      ...timestamps,
    };
    if (path.endsWith("/deployments"))
      return route.fulfill({
        status: route.request().method() === "POST" ? 201 : 200,
        json:
          route.request().method() === "POST"
            ? { deployment }
            : { deployments: [], pagination, can_manage: true },
      });
    if (path.endsWith("/logs"))
      return route.fulfill({ json: { logs: [], pagination } });
    if (path.endsWith("/deployments/deployment-1"))
      return route.fulfill({ json: { deployment } });
    return route.fulfill({
      json: {
        site: { id: "site-1", name: "docs", status: "active", ...timestamps },
      },
    });
  });
  await page.goto(`${base}/sites/site-1`);
  await page.getByLabel("Select site deployment archive").setInputFiles({
    name: "site.zip",
    mimeType: "application/zip",
    buffer: Buffer.from("archive"),
  });
  await expect(page).toHaveURL(/\/sites\/site-1\/deployments\/deployment-1$/);
  await expect(
    page.getByRole("heading", { name: "Version 1", exact: true }),
  ).toBeVisible();
});
