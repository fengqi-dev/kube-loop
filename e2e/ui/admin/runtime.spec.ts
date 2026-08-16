import { expect, test, type Page } from "@playwright/test";

test("stops a live desktop task and session and changes Relay state", async ({
  page,
}) => {
  await signIn(page);

  await page.getByRole("button", { name: "运行资源 / Runtime" }).click();
  const tasks = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Tasks" }),
  });
  const task = tasks.getByRole("row").nth(1);
  await expect(task).toBeVisible();
  await task.getByRole("button", { name: "Stop" }).click();
  await confirm(page, "Stop live desktop task from UI end-to-end test");
  await expect(tasks.getByText(/stopping|stopped/i).first()).toBeVisible();

  const relays = page.locator("section").filter({
    has: page.getByRole("heading", { name: "Relays" }),
  });
  const relay = relays.getByRole("row").nth(1);
  await expect(relay).toBeVisible();
  await relay.getByRole("button", { name: "Drain" }).click();
  await confirm(page, "Drain Relay from UI end-to-end test");
  await expect(relays.getByRole("button", { name: "Recover" }).first()).toBeVisible();
  await relays.getByRole("button", { name: "Recover" }).first().click();
  await confirm(page, "Recover Relay from UI end-to-end test");
  await expect(relays.getByRole("button", { name: "Drain" }).first()).toBeVisible();

  await page.getByRole("button", { name: "会话 / Sessions" }).click();
  const session = page.getByRole("row").filter({ hasText: "active" }).first();
  await expect(session).toBeVisible();
  await session.getByRole("button", { name: "Stop" }).click();
  await confirm(page, "Stop live desktop session from UI end-to-end test");
  await expect(page.getByRole("row").filter({ hasText: "stopped" }).first()).toBeVisible();
});

async function signIn(page: Page) {
  await page.goto("/api/admin/ui/");
  await page.getByRole("button", { name: /local account/i }).click();
  await page
    .getByLabel("Username")
    .fill(required("KUBELOOP_UI_E2E_ADMIN_USERNAME"));
  await page
    .getByLabel("Password")
    .fill(required("KUBELOOP_UI_E2E_ADMIN_PASSWORD"));
  await page.getByRole("button", { name: /Continue|Allow/ }).click();
  await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
}

async function confirm(page: Page, reason: string) {
  await page.getByLabel("Reason").fill(reason);
  await page.getByRole("button", { name: "Confirm" }).click();
}

function required(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
