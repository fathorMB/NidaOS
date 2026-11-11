import { defineConfig } from '@playwright/test';

// The browser test of the installer page (milestone M-07). It runs against
// "nida-installer --demo" (disks of the mockup, simulated installation):
//
//   go run ./cmd/nida-installer --demo --addr 127.0.0.1:8090   (in core/)
//   NIDA_INSTALLER_URL=http://127.0.0.1:8090 npx playwright test -c playwright.installer.config.ts
//
// NIDA_OUT is the folder of the screenshots (optional).
export default defineConfig({
  testDir: './e2e-installer',
  timeout: 120_000,
  expect: { timeout: 20_000 },
  workers: 1,
  reporter: [['list']],
  outputDir: process.env.NIDA_OUT ? `${process.env.NIDA_OUT}/playwright-installer` : 'test-results-installer',
  use: {
    baseURL: process.env.NIDA_INSTALLER_URL ?? 'http://127.0.0.1:8090',
    viewport: { width: 1280, height: 860 },
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
});
