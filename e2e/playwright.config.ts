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
  projects: [
    {
      name: "chromium",
      // Rooms start playback without a click, as a viewer who already
      // interacted with the page would allow.
      use: { ...devices["Desktop Chrome"], launchOptions: { args: ["--autoplay-policy=no-user-gesture-required"] } },
    },
    // The key flows (tagged @cross) also run in Firefox and WebKit.
    {
      name: "firefox",
      grep: /@cross/,
      use: { ...devices["Desktop Firefox"], launchOptions: { firefoxUserPrefs: {
            "media.autoplay.default": 0,
            "media.autoplay.blocking_policy": 0,
            // Firefox's own popups over password fields (the insecure-http
            // warning, password generation, saved logins) sometimes take the
            // click meant for Register; a test profile needs none of them.
            "security.insecure_field_warning.contextual.enabled": false,
            "signon.rememberSignons": false,
            "signon.generation.enabled": false,
            "signon.autofillForms": false,
          } } },
    },
    // WebKit decodes VP9 in software here, so it catches sync bugs that
    // only slow seeks expose; it is still not Safari: check that by hand.
    { name: "webkit", grep: /@cross/, use: { ...devices["Desktop Safari"] } },
  ],
});
