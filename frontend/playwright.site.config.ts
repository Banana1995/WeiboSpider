import { defineConfig } from "@playwright/test";
import options from "./playwright.options.config";
export default defineConfig({
  ...options,
  testMatch: "**/site.e2e.ts",
  webServer: Array.isArray(options.webServer)
    ? options.webServer.filter((server) => server.name !== "Quote SDK fixture")
    : options.webServer,
});
