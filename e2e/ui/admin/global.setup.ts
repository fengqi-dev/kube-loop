import { chromium, type FullConfig } from "@playwright/test";
import fs from "node:fs/promises";
import path from "node:path";

export default async function globalSetup(config: FullConfig) {
  const project = config.projects[0];
  const baseURL = String(project.use.baseURL);
  const storageState = String(project.use.storageState);
  const username = required("KUBELOOP_UI_E2E_ADMIN_USERNAME");
  const password = required("KUBELOOP_UI_E2E_ADMIN_PASSWORD");
  const bootstrapToken = process.env.KUBELOOP_UI_E2E_BOOTSTRAP_TOKEN?.trim();
  await fs.mkdir(path.dirname(storageState), { recursive: true });
  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext({ ignoreHTTPSErrors: true });
  await context.addInitScript(() =>
    localStorage.setItem("kubeloop.admin.locale", "en-US"),
  );
  const page = await context.newPage();
  try {
    await page.goto(`${baseURL}/api/admin/ui/`, {
      waitUntil: "domcontentloaded",
    });
    if (bootstrapToken) {
      await page.getByRole("button", { name: "Initialize new IAM" }).click();
      await page.getByLabel("One-time bootstrap token").fill(bootstrapToken);
      await page.getByLabel("Admin username").fill(username);
      await page.getByLabel("Display name").fill("UI E2E Administrator");
      await page.getByLabel("Email").fill("ui-e2e-admin@example.test");
      await page.getByLabel("Initial password").fill(password);
      await page.getByRole("button", { name: "Create platform admin" }).click();
    }
    await page.getByLabel("Username").fill(username);
    await page.getByLabel("Password").fill(password);
    await page.getByRole("button", { name: /Continue|Allow/ }).click();
    await page.waitForURL(/\/api\/admin\/ui\/(?:#\/overview)?$/);
    await page.getByRole("heading", { name: "Overview" }).waitFor();
    await context.storageState({ path: storageState });
  } catch (error) {
    await page.screenshot({
      path: path.join(path.dirname(storageState), "global-setup-failure.png"),
      fullPage: true,
    });
    throw error;
  } finally {
    await browser.close();
  }
}

function required(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
