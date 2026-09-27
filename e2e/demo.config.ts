import { defineConfig, devices } from "@playwright/test";

// Films the README media (`just demo-media`) against the production
// stack in compose.demo.yaml; not part of the test suite.
export default defineConfig({
  testDir: "./demo",
  timeout: 20 * 60_000,
  expect: { timeout: 15_000 },
  workers: 1,
  reporter: [["list"]],
  outputDir: "demo-results",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: process.env.BASE_URL,
    colorScheme: "dark",
    launchOptions: { args: ["--autoplay-policy=no-user-gesture-required"] },
  },
});
