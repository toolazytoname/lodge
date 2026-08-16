import { defineConfig } from "@playwright/test";

const fixturePort = Number(process.env.LODGE_FIXTURE_PORT || "4173");
const fixtureOrigin = `http://127.0.0.1:${fixturePort}`;

export default defineConfig({
  testDir: "./frontend/tests",
  snapshotPathTemplate: "{testDir}/__screenshots__/{testFilePath}/{arg}{ext}",
  fullyParallel: false,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI ? [["github"], ["list"]] : "list",
  expect: {
    timeout: 5_000,
    toHaveScreenshot: {
      animations: "disabled",
      caret: "hide",
      maxDiffPixelRatio: 0.04,
      threshold: 0.25,
    },
  },
  use: {
    baseURL: fixtureOrigin,
    colorScheme: "dark",
    locale: "zh-CN",
    timezoneId: "Asia/Shanghai",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "node scripts/web-fixture-server.mjs",
    url: `${fixtureOrigin}/api/session`,
    reuseExistingServer: false,
    timeout: 15_000,
  },
  projects: [
    {
      name: "chromium",
      use: { browserName: "chromium" },
    },
  ],
});
