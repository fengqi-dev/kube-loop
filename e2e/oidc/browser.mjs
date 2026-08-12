import { chromium } from "playwright";
import path from "node:path";

const [authorizationURL, username, password, artifactDirectory] = process.argv.slice(2);
if (!authorizationURL || !username || !password || !artifactDirectory) {
  throw new Error("authorization URL, username, password and artifact directory are required");
}

const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({ ignoreHTTPSErrors: true });
await context.tracing.start({ screenshots: true, snapshots: true, sources: true });
const page = await context.newPage();

try {
  await page.goto(authorizationURL, { waitUntil: "domcontentloaded", timeout: 30_000 });
  await page.locator("#username").fill(username);
  await page.locator("#password").fill(password);
  await page.locator("#kc-login").click();
  await page.waitForURL(/http:\/\/127\.0\.0\.1:\d+\/callback/, {
    waitUntil: "domcontentloaded",
    timeout: 30_000,
  });
  await page.getByText("KubeLoop login complete").waitFor({ timeout: 10_000 });
  await page.screenshot({
    path: path.join(artifactDirectory, "login-complete.png"),
    fullPage: true,
  });
} catch (error) {
  await page.screenshot({
    path: path.join(artifactDirectory, "login-failure.png"),
    fullPage: true,
  });
  throw error;
} finally {
  await context.tracing.stop({ path: path.join(artifactDirectory, "playwright-trace.zip") });
  await browser.close();
}
