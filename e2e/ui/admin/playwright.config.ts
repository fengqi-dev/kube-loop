import { defineConfig } from "@playwright/test";
import path from "node:path";

const artifacts =
  process.env.KUBELOOP_UI_E2E_ARTIFACTS ||
  path.resolve("../../../build/ui-e2e/admin");

export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  globalSetup: "./global.setup.ts",
  timeout: 45_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  outputDir: path.join(artifacts, "results"),
  reporter: [
    ["line"],
    ["html", { outputFolder: path.join(artifacts, "report"), open: "never" }],
  ],
  use: {
    baseURL: required("KUBELOOP_UI_E2E_BASE_URL"),
    storageState: path.join(artifacts, "admin-storage.json"),
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
});

function required(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
