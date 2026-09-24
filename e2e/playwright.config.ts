import { defineConfig, devices } from "@playwright/test";

// Runs inside the compose e2e profile (`just e2e`): the app is served by
// the e2e-frontend service against its own database.
export default defineConfig({
  testDir: "./tests",
  timeout: 30_000,
  expect: { timeout: 7_000 },
  fullyParallel: true,
  workers: 4,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"], ["html", { open: "never", outputFolder: "report" }]],
  use: {
    baseURL: process.env.BASE_URL ?? "http://localhost:5173",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  outputDir: "results",
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
