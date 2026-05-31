import { defineConfig, devices } from "@playwright/test";

const loopbackNoProxy = ["127.0.0.1", "localhost", "::1"];
const currentNoProxy = process.env.NO_PROXY || process.env.no_proxy || "";
const mergedNoProxy = Array.from(
  new Set([
    ...currentNoProxy
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean),
    ...loopbackNoProxy,
  ]),
).join(",");

process.env.NO_PROXY = mergedNoProxy;
process.env.no_proxy = mergedNoProxy;

export default defineConfig({
  testDir: "./e2e",
  timeout: 30_000,
  expect: {
    timeout: 5_000,
  },
  fullyParallel: false,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:3001",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "npm run dev:e2e",
    reuseExistingServer: true,
    timeout: 120_000,
    url: "http://127.0.0.1:3001",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
