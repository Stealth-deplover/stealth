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
  withInvitation?: boolean;
  withMember?: boolean;
  withUser?: boolean;
};

async function installFixtures(page: Page, options: FixtureOptions = {}) {
  const canManage = options.canManage ?? true;
  const memberships =
    options.withMember === false
      ? []
      : [
          {
            organization_id: organization.id,
            account_id: "member-1",
            email: "member@example.com",
            role: "developer",
            created_at: "2026-01-02T00:00:00Z",
          },
        ];
  const invitations = options.withInvitation
    ? [
        {
          id: "invitation-1",
          organization_id: organization.id,
          email: "pending@example.com",
          role: "viewer",
          invited_by_account_id: account.id,
          invited_by_email: account.email,
          status: "pending",
          expires_at: "2026-02-01T00:00:00Z",
          accepted_at: null,
          revoked_at: null,
          created_at: "2026-01-03T00:00:00Z",
        },
      ]
    : [];
  const users = options.withUser
    ? [
        {
          id: "user-1",
          project_id: project.id,
          email: "app@example.com",
          name: "Application user",
          status: "active",
          email_verified: true,
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

    if (path === "/v1/organizations/org-1/memberships") {
      if (method === "GET")
        return respond({ memberships, pagination, can_manage: canManage });
      if (method === "POST") {
        const body = request.postDataJSON() as { email: string; role: string };
        memberships.push({
          organization_id: organization.id,
          account_id: "member-created",
          email: body.email,
          role: body.role,
          created_at: "2026-01-04T00:00:00Z",
        });
        return respond({ membership: memberships.at(-1) }, 201);
      }
    }
    if (path === "/v1/organizations/org-1/memberships/member-1") {
      if (method === "PATCH") {
        const body = request.postDataJSON() as { role: string };
        memberships[0].role = body.role;
        return respond({ membership: memberships[0] });
      }
      if (method === "DELETE") {
        memberships.splice(0, 1);
        return route.fulfill({ status: 204 });
      }
    }

    if (path === "/v1/organizations/org-1/invitations") {
      if (method === "GET")
        return respond({ invitations, pagination, can_manage: canManage });
      if (method === "POST") {
        const body = request.postDataJSON() as { email: string; role: string };
        const invitation = {
          id: "invitation-created",
          organization_id: organization.id,
          email: body.email,
          role: body.role,
          invited_by_account_id: account.id,
          invited_by_email: account.email,
          status: "pending",
          expires_at: "2026-02-01T00:00:00Z",
          accepted_at: null,
          revoked_at: null,
          created_at: "2026-01-04T00:00:00Z",
        };
        invitations.push(invitation);
        return respond({ invitation, delivery: "sent" }, 201);
      }
    }
    if (path.startsWith("/v1/organizations/org-1/invitations/")) {
      if (method === "DELETE") {
        const invitationId = path.split("/").at(-1);
        const index = invitations.findIndex(
          (invitation) => invitation.id === invitationId,
        );
        if (index >= 0) invitations.splice(index, 1);
        return route.fulfill({ status: 204 });
      }
    }

    if (path === "/v1/projects/project-1/users") {
      if (method === "GET")
        return respond({ users, pagination, can_manage: canManage });
      if (method === "POST") {
        const body = request.postDataJSON() as {
          email: string;
          name?: string | null;
        };
        const user = {
          id: "user-created",
          project_id: project.id,
          email: body.email,
          name: body.name ?? null,
          status: "active",
          email_verified: false,
          created_at: "2026-01-04T00:00:00Z",
          updated_at: "2026-01-04T00:00:00Z",
        };
        users.push(user);
        return respond({ user }, 201);
      }
    }
    if (path === "/v1/projects/project-1/users/user-created") {
      if (method === "GET") return respond({ user: users.at(-1) });
      if (method === "DELETE") {
        users.splice(0, users.length);
        return route.fulfill({ status: 204 });
      }
    }
    if (path === "/v1/projects/project-1/users/user-created/status") {
      if (method === "PATCH") {
        const body = request.postDataJSON() as { status: string };
        const user = users.at(-1);
        if (user) user.status = body.status;
        return respond({ user });
      }
    }

    return respond({});
  });
}

test("creates and revokes an organization invitation", async ({ page }) => {
  await installFixtures(page);
  await page.goto("/organizations/org-1/members");
  await expect(page.getByRole("heading", { name: "Members" })).toBeVisible();
  await expect(page.getByText("member@example.com")).toBeVisible();
  await page.getByRole("link", { name: "Invitations" }).click();
  await expect(
    page.getByRole("heading", { name: "Invitations", exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Invite member" }).first().click();
  const createDialog = page.getByRole("dialog");
  await createDialog
    .getByLabel("Email", { exact: true })
    .fill("new@example.com");
  await createDialog.getByRole("button", { name: "Create invitation" }).click();
  await expect(
    page.getByText("new@example.com", { exact: true }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Revoke" }).click();
  const revokeDialog = page.getByRole("dialog");
  await revokeDialog.getByRole("button", { name: "Revoke invitation" }).click();
  await expect(
    page.getByRole("heading", { name: "No pending invitations" }),
  ).toBeVisible();
});

test("keeps organization mutations hidden for read-only members", async ({
  page,
}) => {
  await installFixtures(page, { canManage: false, withInvitation: true });
  await page.goto("/organizations/org-1/members");
  await expect(page.getByText("Read-only")).toBeVisible();
  await expect(page.getByRole("button", { name: "Invitations" })).toHaveCount(
    0,
  );
  await expect(page.getByRole("button", { name: "Change role" })).toHaveCount(
    0,
  );
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0);

  await page.goto("/organizations/org-1/invitations");
  await expect(page.getByText("Read-only")).toBeVisible();
  await expect(page.getByRole("button", { name: "Invite member" })).toHaveCount(
    0,
  );
  await expect(page.getByRole("button", { name: "Revoke" })).toHaveCount(0);
});

test("shows contextual empty states for members and project users", async ({
  page,
}) => {
  await installFixtures(page, { withMember: false });
  await page.goto("/organizations/org-1/members");
  await expect(
    page.getByRole("heading", { name: "No members yet" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Invite member" }),
  ).toBeVisible();

  await page.goto("/organizations/org-1/projects/project-1/users");
  await expect(
    page.getByRole("heading", { name: "No application users yet" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Create user" }).first(),
  ).toBeVisible();
});

test("creates, disables, and deletes a project user", async ({ page }) => {
  await installFixtures(page);
  await page.goto("/organizations/org-1/projects/project-1/users");
  await page.getByRole("button", { name: "Create user" }).first().click();
  const createDialog = page.getByRole("dialog");
  await createDialog
    .getByLabel("Email", { exact: true })
    .fill("app@example.com");
  await createDialog
    .getByLabel("Temporary password", { exact: true })
    .fill("a-secure-password");
  await createDialog
    .getByRole("button", { name: "Create user", exact: true })
    .click();

  await expect(page).toHaveURL(/\/users\/user-created$/);
  await expect(
    page.getByRole("heading", { name: "app@example.com" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Disable user" }).click();
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Disable user", exact: true })
    .click();
  await expect(page.getByRole("button", { name: "Enable user" })).toBeVisible();

  await page.getByRole("button", { name: "Delete user", exact: true }).click();
  const deleteDialog = page.getByRole("dialog");
  await deleteDialog
    .getByRole("button", { name: "Delete user", exact: true })
    .click();
  await expect(page).toHaveURL(/\/projects\/project-1\/users$/);
  await expect(
    page.getByRole("heading", { name: "No application users yet" }),
  ).toBeVisible();
});

test("accepts an email-bound organization invitation through the Go API", async ({
  page,
}) => {
  const token = "a".repeat(43);
  let requestBody: unknown;
  await page.route("**/v1/**", async (route) => {
    const request = route.request();
    if (
      new URL(request.url()).pathname ===
        "/v1/organization-invitations/accept" &&
      request.method() === "POST"
    ) {
      requestBody = request.postDataJSON();
      return route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          membership: {
            organization_id: "org-accepted",
            account_id: "account-1",
            email: account.email,
            role: "developer",
            created_at: "2026-01-04T00:00:00Z",
          },
        }),
      });
    }
    return route.fulfill({ status: 404, json: {} });
  });

  await page.goto("/accept-invitation?token=" + token);
  await expect(
    page.getByRole("heading", { name: "Review invitation" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Accept invitation" }).click();
  await expect(
    page.getByRole("heading", { name: "Invitation accepted" }),
  ).toBeVisible();
  expect(requestBody).toEqual({ token });
  await expect(
    page.getByRole("button", { name: "Open organization" }),
  ).toBeVisible();
});
