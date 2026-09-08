import { expect, test } from "@playwright/test";

test("login form is available without an API dependency", async ({ page }) => {
  await page.goto("/login");
  await expect(
    page.getByRole("heading", { name: "Sign in to Stealth" }),
  ).toBeVisible();
  await expect(page.getByLabel("Email")).toBeVisible();
  await expect(page.getByLabel("Password")).toBeVisible();
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByText("Enter a valid email address.")).toBeVisible();
});

test("authentication links are discoverable", async ({ page }) => {
  await page.goto("/login");
  await expect(
    page.getByRole("link", { name: "Forgot password?" }),
  ).toHaveAttribute("href", "/recovery");
  await expect(
    page.getByRole("link", { name: "Create an account" }),
  ).toHaveAttribute("href", "/register");
});

test("unsafe auth next paths fall back to organizations", async ({ page }) => {
  for (const unsafeNext of ["//evil.example", "https://evil.example"]) {
    await page.goto("/login?next=" + encodeURIComponent(unsafeNext));
    const registerHref = await page
      .getByRole("link", { name: "Create an account" })
      .getAttribute("href");
    expect(
      new URL(registerHref ?? "", "http://127.0.0.1").searchParams.get("next"),
    ).toBe("/organizations");

    await page.goto("/register?next=" + encodeURIComponent(unsafeNext));
    const signInHref = await page
      .getByRole("link", { name: "Sign in" })
      .getAttribute("href");
    expect(
      new URL(signInHref ?? "", "http://127.0.0.1").searchParams.get("next"),
    ).toBe("/organizations");
  }
});

test("preserves an invitation through registration for a new account", async ({
  page,
}) => {
  const token = "b".repeat(43);
  const invitationPath = "/accept-invitation?token=" + token;
  const account = {
    id: "account-new",
    email: "new-member@example.com",
    email_verified: false,
    created_at: "2026-01-01T00:00:00Z",
  };
  let registered = false;

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

    if (path === "/v1/account" && method === "GET") {
      return registered
        ? respond({ account })
        : respond(
            {
              error: {
                code: "unauthorized",
                message: "authentication is required",
              },
            },
            401,
          );
    }
    if (path === "/v1/organization-invitations/accept" && method === "POST") {
      if (!registered)
        return respond(
          {
            error: {
              code: "unauthorized",
              message: "authentication is required",
            },
          },
          401,
        );
      return respond({
        membership: {
          organization_id: "org-invited",
          account_id: account.id,
          email: account.email,
          role: "developer",
          created_at: "2026-01-04T00:00:00Z",
        },
      });
    }
    if (path === "/v1/account/registrations" && method === "POST") {
      registered = true;
      return respond(
        {
          account,
          organization: {
            id: "org-personal",
            name: "Personal organization",
            slug: "personal",
            created_at: "2026-01-01T00:00:00Z",
          },
        },
        201,
      );
    }
    return respond({});
  });

  await page.goto(invitationPath);
  await expect(
    page.getByRole("heading", { name: "Review invitation" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Accept invitation" }).click();
  await expect(
    page.getByRole("heading", { name: "Invitation could not be accepted" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "Sign in to accept" }).click();
  await expect(page).toHaveURL(/\/login\?next=/);
  expect(new URL(page.url()).searchParams.get("next")).toBe(invitationPath);

  await page.getByRole("link", { name: "Create an account" }).click();
  await expect(page).toHaveURL(/\/register\?next=/);
  expect(new URL(page.url()).searchParams.get("next")).toBe(invitationPath);

  await page.getByLabel("Email").fill(account.email);
  await page.getByLabel("Password").fill("a-secure-password");
  await page.getByRole("button", { name: "Create account" }).click();
  await expect(page).toHaveURL(/\/accept-invitation\?token=/);
  expect(new URL(page.url()).searchParams.get("token")).toBe(token);
  const storedValues = await page.evaluate(() => [
    ...Object.values(localStorage),
    ...Object.values(sessionStorage),
  ]);
  expect(storedValues.some((value) => value.includes(token))).toBe(false);
  await expect(
    page.getByRole("heading", { name: "Review invitation" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "Accept invitation" }).click();
  await expect(
    page.getByRole("heading", { name: "Invitation accepted" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Open organization" }).click();
  await expect(page).toHaveURL(/\/organizations\/org-invited$/);
});
