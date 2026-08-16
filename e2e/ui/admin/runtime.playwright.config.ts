import { defineConfig } from "@playwright/test";
import path from "node:path";

const artifacts =
  process.env.KUBELOOP_UI_E2E_ARTIFACTS ||
  path.resolve("../../../build/ui-e2e/admin-runtime");

export default defineConfig({
  testDir: ".",
  testMatch: /runtime\.spec\.ts/,
  timeout: 60_000,
  expect: { timeout: 20_000 },
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: 0,
  workers: 1,
  outputDir: path.join(artifacts, "results"),
  reporter: [["line"]],
  use: {
    baseURL: required("KUBELOOP_UI_E2E_BASE_URL"),
    ignoreHTTPSErrors: true,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});

function required(name: string) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);
  return value;
}
