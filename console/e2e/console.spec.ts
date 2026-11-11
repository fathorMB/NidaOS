import { expect, test, type Page } from '@playwright/test';

// The browser test of the web console (milestone M-06, task T-10).

const password = process.env.NIDA_PASSWORD ?? 'mock password';
const out = process.env.NIDA_OUT;
const shot = async (page: Page, name: string) => {
  if (out) await page.screenshot({ path: `${out}/console-${name}.png`, fullPage: true });
};

async function signIn(page: Page) {
  await page.goto('/');
  await page.getByLabel('Owner password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible();
}

const menu = (page: Page, name: string) =>
  page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: new RegExp(`^${name}`) });

test.describe.configure({ mode: 'serial' });

test('the sign-in refuses a wrong password and accepts the owner password', async ({ page }) => {
  await page.goto('/apps');
  await expect(page.getByLabel('Owner password')).toBeVisible();
  await shot(page, 'signin');
  await page.getByLabel('Owner password').fill('not the password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('not correct');
  await page.getByLabel('Owner password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('heading', { name: 'Apps' })).toBeVisible();
});

test('the overview shows the live metrics and the live log', async ({ page }) => {
  await signIn(page);
  await expect(page.locator('.live').first()).toHaveText('Live');
  await expect(page.getByTestId('meter-Processor')).toHaveText(/^\d+$/);
  await expect(page.getByTestId('meter-Memory')).toHaveText(/^\d+\.\d$/);
  // The values change without a reload (decision D8).
  const seen = new Set<string>();
  for (let i = 0; i < 15 && seen.size < 2; i++) {
    seen.add(`${await page.getByTestId('meter-Memory').textContent()} ${await page.getByTestId('meter-Processor').textContent()} ${await page.getByTestId('meter-Network in').textContent()}`);
    await page.waitForTimeout(1500);
  }
  expect(seen.size).toBeGreaterThan(1);
  await expect(page.getByTestId('log-rows').locator('time').first()).toBeVisible();
  await shot(page, 'overview');
});

test('the pages of the menu open', async ({ page }) => {
  await signIn(page);
  await menu(page, 'Storage').click();
  await expect(page.getByRole('heading', { name: 'Storage' })).toBeVisible();
  await expect(page.getByRole('table', { name: 'Tiers' })).toContainText('Performance');
  await expect(page.getByRole('table', { name: 'Tiers' })).toContainText('Mounted');
  await shot(page, 'storage');

  await menu(page, 'Updates').click();
  await expect(page.getByRole('table', { name: 'System versions' })).toContainText('In operation');
  await shot(page, 'updates');

  await menu(page, 'Runners').click();
  await expect(page.getByRole('heading', { name: 'GitHub runners' })).toBeVisible();
  await shot(page, 'runners');

  await menu(page, 'Databases').click();
  await expect(page.getByRole('heading', { name: 'Databases' })).toBeVisible();

  await menu(page, 'Logs').click();
  await expect(page.getByTestId('log-rows').locator('time').first()).toBeVisible();
  await page.getByRole('button', { name: 'Pause' }).click();
  await expect(page.locator('.live.paused')).toBeVisible();
  await page.getByLabel('Minimum level').selectOption('warning');
  await page.getByRole('button', { name: 'Continue' }).click();
  await page.getByRole('button', { name: 'Search the log store' }).click();
  await expect(page.getByRole('button', { name: 'Search the log store' })).toBeEnabled();
  await shot(page, 'logs');
});

test('the owner adds an app and removes it in the console', async ({ page }) => {
  const name = `console-e2e-${Date.now() % 100000}`;
  await signIn(page);
  await menu(page, 'Apps').click();
  await page.getByRole('link', { name: 'Add an app' }).click();
  await page.getByRole('textbox', { name: 'Name' }).fill(name);
  await shot(page, 'app-new');
  await page.getByRole('button', { name: /^Add the app/ }).click();
  await expect(page.getByRole('heading', { name })).toBeVisible();
  await expect(page.getByText('No version yet').first()).toBeVisible();

  await page.getByRole('tab', { name: 'Variables and secrets' }).click();
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('MODE');
  await page.getByRole('textbox', { name: 'Value', exact: true }).fill('test');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('table', { name: 'Variables' })).toContainText('MODE');

  await page.getByRole('tab', { name: 'Stop or remove' }).click();
  await page.getByRole('button', { name: 'Remove…' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel('Confirmation text').fill(name);
  await dialog.getByRole('button', { name: 'Remove' }).click();
  await expect(page.getByRole('heading', { name: 'Apps' })).toBeVisible();
  await expect(page.getByRole('table', { name: 'Apps' })).not.toContainText(name);
});

test('an app page shows its live data', async ({ page }) => {
  await signIn(page);
  await menu(page, 'Apps').click();
  await page.getByRole('table', { name: 'Apps' }).locator('tbody tr.open').first().click();
  await expect(page.getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('meter-Memory')).toHaveText(/^\d+$/);
  await shot(page, 'app');
  await page.getByRole('tab', { name: 'Deploys' }).click();
  await expect(page.getByRole('heading', { name: 'The GitHub repository' })).toBeVisible();
});

test('the page tells when the live stream stops', async ({ page }) => {
  await signIn(page);
  await expect(page.locator('.live').first()).toHaveText('Live');
  // The stream of the metrics cannot connect again: the page must tell it.
  await page.route('**/api/v1/metrics/stream', (route) => route.abort());
  await page.reload();
  await expect(page.getByText('The live data stopped.')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText(/Stopped · last data/).first()).toBeVisible();
  await shot(page, 'stream-stopped');
});

test('the theme follows the choice of the owner', async ({ page }) => {
  await signIn(page);
  await page.getByLabel('Theme').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await shot(page, 'dark');
  await page.getByLabel('Theme').selectOption('light');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await shot(page, 'light');
  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByLabel('Owner password')).toBeVisible();
});
