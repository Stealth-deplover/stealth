import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3100";
const useProductionServer = Boolean(
  process.env.CI || process.env.PLAYWRIGHT_USE_PRODUCTION,
);
const productionServerCommand = [
  "mkdir -p .next/standalone/.next",
  "if [ ! -e .next/standalone/.next/static ]; then cp -R .next/static .next/standalone/.next/static; fi",
  "if [ -d public ] && [ ! -e .next/standalone/public ]; then cp -R public .next/standalone/public; fi",
  `HOSTNAME=127.0.0.1 PORT=${port} node .next/standalone/server.js`,
].join(" && ");

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 2 : 0,
  reporter: "html",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://127.0.0.1:${port}`,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "on-first-retry",
  },
  webServer: {
    command: useProductionServer
      ? productionServerCommand
      : `npm run dev -- --port ${port}`,
    url: `http://127.0.0.1:${port}/login`,
    reuseExistingServer: !process.env.CI,
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
