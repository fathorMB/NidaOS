import { defineConfig } from '@playwright/test';

// The browser test of the console (task T-10). It runs against a real
// NidaOS (the VM test) or against the fake API (dev/mock.mjs).
//
//   NIDA_URL       the address of the console, for example https://nidaos.local:8443
//   NIDA_PASSWORD  the owner password
//   NIDA_RESOLVE   optional: the address for the name of NIDA_URL, for
//                  example 127.0.0.1 (the port forward of the VM)
//   NIDA_OUT       optional: the folder for the screenshots
const url = new URL(process.env.NIDA_URL ?? 'http://127.0.0.1:8480');
const resolve = process.env.NIDA_RESOLVE;

export default defineConfig({
  testDir: './e2e',
  timeout: 90_000,
  expect: { timeout: 20_000 },
  workers: 1,
  retries: 0,
  reporter: [['list']],
  outputDir: process.env.NIDA_OUT ? `${process.env.NIDA_OUT}/playwright` : 'test-results',
  use: {
    baseURL: url.origin,
    // The VM test examines the certificate with curl and the NidaOS
    // authority. The browser of the test does not have the authority.
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 860 },
    screenshot: 'only-on-failure',
    trace: 'off',
    launchOptions: resolve ? { args: [`--host-resolver-rules=MAP ${url.hostname} ${resolve}`] } : {},
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
