import { expect, test, type Page } from "@playwright/test";

async function signInIfNeeded(page: Page) {
  const signOut = page.getByRole("button", { name: "Sign out" });
  if (await signOut.isVisible()) return;

  await expect(
    page.getByRole("heading", { name: "Sign in to Admin" }),
  ).toBeVisible();
  const username = process.env.KUBELOOP_UI_E2E_ADMIN_USERNAME;
  const password = process.env.KUBELOOP_UI_E2E_ADMIN_PASSWORD;
  if (!username || !password) {
    throw new Error("admin E2E credentials are required");
  }
  await page.getByRole("button", { name: "Local account" }).click();
  const usernameField = page.getByLabel("Username");
  const overview = page.getByRole("heading", { name: "Overview" });
  await expect(usernameField.or(overview)).toBeVisible();
  if (await overview.isVisible()) return;
  await usernameField.fill(username);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /Continue|Allow/ }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
}

test.beforeEach(async ({ page }) => {
  await page.goto("/api/admin/ui/");
});

test("renders the real overview and every management surface", async ({
  page,
}) => {
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
  await expect(
    page.getByText("Active sessions", { exact: true }),
  ).toBeVisible();
  const pages = [
    ["用户 / Users", "Users"],
    ["用户组 / Groups", "Groups"],
    ["邀请 / Invitations", "Invitations"],
    ["OAuth Clients", "OAuth Clients"],
    ["策略 / Policies", "Effective policies"],
    ["会话 / Sessions", "Sessions"],
    ["OAuth Grants", "OAuth Grants"],
    ["审计 / Audit", "Audit"],
    ["运行资源 / Runtime", "Runtime resources"],
  ] as const;
  for (const [navigation, heading] of pages) {
    await page.getByRole("button", { name: navigation }).click();
    await expect(page.getByRole("heading", { name: heading })).toBeVisible();
    await expect(
      page.getByText(/This resource returns only data visible/),
    ).toHaveCount(0);
  }
});

test("manages the IAM directory and OAuth clients through the real API", async ({
  page,
}) => {
  const suffix = Date.now();
  const name = `ui-e2e-${suffix}`;
  await page.getByRole("button", { name: "用户组 / Groups" }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.getByLabel("Name").fill(name);
  await page
    .getByLabel("Description")
    .fill("Created by the real UI end-to-end suite");
  await page
    .getByLabel("Change reason (at least 8 characters)")
    .fill("Create group from UI end-to-end test");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByRole("cell", { name, exact: true })).toBeVisible();

  await page.getByLabel("Select group").selectOption({ label: name });
  const namespaceForm = page
    .getByRole("button", { name: "Add namespace" })
    .locator("xpath=ancestor::form");
  await namespaceForm.getByLabel("Namespace").fill("default");
  await namespaceForm
    .getByLabel("Change reason (at least 8 characters)")
    .fill("Grant default namespace from UI end-to-end test");
  await namespaceForm.getByRole("button", { name: "Add namespace" }).click();
  await expect(page.getByText("default", { exact: true })).toBeVisible();

  const username = `ui-user-${suffix}`;
  await page.getByRole("button", { name: "用户 / Users" }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Display name").fill(`UI User ${suffix}`);
  await page.getByLabel("Email").fill(`${username}@example.test`);
  await page.getByLabel("Initial password").fill(`Ui-User-${suffix}-Password!`);
  await page.getByLabel("Group").selectOption({ label: name });
  await page.getByRole("button", { name: "Save" }).click();
  await expect(
    page.getByText(`UI User ${suffix}`, { exact: true }),
  ).toBeVisible();

  const inviteEmail = `ui-invite-${suffix}@example.test`;
  await page.getByRole("button", { name: "邀请 / Invitations" }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.getByLabel("Email").fill(inviteEmail);
  await page.getByLabel("Group").selectOption({ label: name });
  await page
    .getByLabel("Change reason (at least 8 characters)")
    .fill("Create invitation from UI end-to-end test");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(inviteEmail, { exact: true })).toBeVisible();
  await expect(page.getByText("pending", { exact: true })).toBeVisible();

  const clientId = `ui-e2e-client-${suffix}`;
  await page.getByRole("button", { name: "OAuth Clients" }).click();
  await page.getByRole("button", { name: "Create" }).click();
  await page.getByLabel("Client ID").fill(clientId);
  await page.getByLabel("Name").fill(`UI E2E Client ${suffix}`);
  await page
    .getByLabel("Redirect URI (one per line)")
    .fill("https://client.example.test/callback");
  await page.getByLabel("Authorization Code + PKCE S256").check();
  await page.getByLabel("Refresh Token").check();
  await page
    .getByLabel("Scopes")
    .fill("openid profile offline_access kubeloop.api");
  await page.getByLabel("Public client").check();
  await page
    .getByLabel("Change reason (at least 8 characters)")
    .fill("Create OAuth client from UI end-to-end test");
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText(clientId, { exact: true })).toBeVisible();
});

test("runs recovery and exports audit records", async ({ page }) => {
  await page.getByRole("button", { name: "运行资源 / Runtime" }).click();
  await page.getByRole("button", { name: "Recovery" }).click();
  await page.getByLabel("Reason").fill("Run recovery from UI end-to-end test");
  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("heading", { name: "Run recovery" })).toHaveCount(
    0,
  );

  await page.getByRole("button", { name: "审计 / Audit" }).click();
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export" }).click();
  const artifact = await download;
  expect(artifact.suggestedFilename()).toMatch(/^kubeloop-audit-.+\.ndjson$/);
});

test("rejects password, implicit, and hybrid OAuth grants", async ({
  page,
}) => {
  const origin = new URL(page.url()).origin;
  const redirectURI = `${origin}/api/admin/ui/callback`;
  for (const responseType of ["token", "code token"]) {
    const response = await page.request.get(`${origin}/oauth2/authorize`, {
      maxRedirects: 0,
      params: {
        response_type: responseType,
        client_id: "kubeloop-management",
        redirect_uri: redirectURI,
        scope: "openid profile",
        state: "ui-e2e-invalid-flow",
        nonce: "ui-e2e-invalid-flow",
      },
    });
    const rejected =
      response.status() >= 400 ||
      (response.status() >= 300 &&
        response.status() < 400 &&
        (response.headers().location || "").includes("error="));
    expect(rejected).toBe(true);
  }
  const password = await page.request.post(`${origin}/oauth2/token`, {
    form: {
      grant_type: "password",
      client_id: "kubeloop-management",
      username: "not-used",
      password: "not-used",
    },
  });
  expect(password.status()).toBeGreaterThanOrEqual(400);
});

test("keeps mobile navigation operable", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Open navigation menu" }).click();
  await expect(
    page.getByRole("button", { name: "Close navigation menu" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "审计 / Audit" }).click();
  await expect(page.getByRole("heading", { name: "Audit" })).toBeVisible();
});

test("revokes a live OAuth grant through the management UI", async ({
  page,
}) => {
  await page.getByRole("button", { name: "OAuth Grants" }).click();
  const activeGrant = page.getByRole("row").filter({ hasText: "active" }).first();
  await expect(activeGrant).toBeVisible();
  await activeGrant.getByRole("button", { name: "Revoke" }).click();
  await page
    .getByLabel("Reason")
    .fill("Revoke OAuth grant from UI end-to-end test");
  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in to Admin" }),
  ).toBeVisible();
});

test("signs out and requires a real OAuth login", async ({ page }) => {
  await signInIfNeeded(page);
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(
    page.getByRole("heading", { name: "Sign in to Admin" }),
  ).toBeVisible();
  await expect(
    page.getByText("Authorization Code + PKCE S256").first(),
  ).toBeVisible();
});
