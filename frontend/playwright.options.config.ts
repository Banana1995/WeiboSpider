import { defineConfig } from "@playwright/test";
import { join } from "node:path";
import { tmpdir } from "node:os";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/options.e2e.ts",
  workers: 1,
  fullyParallel: false,
  timeout: 60000,
  expect: { timeout: 12000 },
  outputDir: join(
    process.env.OPTIONS_E2E_ROOT || tmpdir(),
    "options-playwright-results",
  ),
  reporter: "list",
  use: {
    baseURL: "http://127.0.0.1:17173",
    viewport: { width: 1440, height: 1000 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  webServer: [
    {
      name: "Quote SDK fixture",
      command: `${JSON.stringify(process.env.OPTIONS_PYTHON || "python3")} ../services/option-quotes/tests/fixture_server.py`,
      env: { OPTIONS_TEST_MODE: "1" },
      url: "http://127.0.0.1:17053/healthz",
      reuseExistingServer: false,
      timeout: 120000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5000 },
    },
    {
      name: "Isolated Go backend",
      command: "node e2e/options-backend.mjs",
      url: "http://127.0.0.1:17051/healthz",
      reuseExistingServer: false,
      timeout: 120000,
      gracefulShutdown: { signal: "SIGTERM", timeout: 10000 },
    },
    {
      name: "Vue options",
      command: "npm run dev -- --port 17173",
      env: {
        BACKEND_PROXY_URL: "http://127.0.0.1:17051",
        BACKEND_API_TOKEN: "",
      },
      url: "http://127.0.0.1:17173/options",
      reuseExistingServer: false,
      gracefulShutdown: { signal: "SIGTERM", timeout: 5000 },
    },
  ],
});
