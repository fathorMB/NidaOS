import { expect, test, type Page } from '@playwright/test';

// The six steps of the installer (milestone M-07, tasks T-03 to T-06),
// against "nida-installer --demo". The demonstration changes no disk.

const out = process.env.NIDA_OUT;
const shot = async (page: Page, name: string) => {
  if (out) await page.screenshot({ path: `${out}/installer-${name}.png`, fullPage: true });
};
const next = (page: Page) => page.getByRole('button', { name: 'Continue' }).click();
const current = (page: Page) => page.getByRole('list', { name: 'Steps' }).locator('li[aria-current=step]');

test('the operator installs NidaOS with the six steps', async ({ page }) => {
  await page.goto('/');
  // 1 Welcome
  await expect(page.getByRole('heading', { name: 'Install NidaOS on this computer' })).toBeVisible();
  await expect(page.getByText('MSI B85M-G43').first()).toBeVisible();
  await expect(page.getByText('2 SSD · 1 HDD')).toBeVisible();
  await page.getByLabel('Keyboard').selectOption('it');
  await shot(page, 'welcome');
  await next(page);

  // 2 Disks: the plan of the mockup, with a reason for each role.
  await expect(current(page)).toHaveText('Disks');
  await expect(page.getByLabel('Role of Kingston SA400S37')).toHaveValue('system', { timeout: 30_000 });
  await expect(page.getByLabel('Role of SanDisk SDSSDHP128G')).toHaveValue('performance');
  await expect(page.getByLabel('Role of Seagate ST1000DM003')).toHaveValue('standard');
  await expect(page.getByTestId('disk-sdb')).toContainText('486 MB/s');
  await expect(page.getByTestId('disk-sdb')).toContainText('the fastest disk');
  await expect(page.getByTestId('disk-sdd')).toContainText('the installation media');
  await shot(page, 'disks');
  // A change by hand: two disks with the same role stop the next step.
  await page.getByLabel('Role of Seagate ST1000DM003').selectOption('system');
  await expect(page.getByRole('alert')).toContainText('same role');
  await expect(page.getByRole('button', { name: 'Continue' })).toBeDisabled();
  await page.getByRole('button', { name: 'Use the proposed plan' }).click();
  await expect(page.getByLabel('Role of Seagate ST1000DM003')).toHaveValue('standard');
  await next(page);

  // 3 Access: one owner password, two times (decision D34).
  await expect(current(page)).toHaveText('Access');
  await page.getByLabel('Computer name').fill('home');
  await page.getByLabel('Owner password').fill('short');
  await next(page);
  await expect(page.getByText('Use 12 characters or more.')).toBeVisible();
  await page.getByLabel('Owner password').fill('a long owner password');
  await page.getByLabel('Repeat the password').fill('a long owner password');
  await shot(page, 'access');
  await next(page);

  // 4 Network: a fixed address must be in the network of the router.
  await expect(current(page)).toHaveText('Network');
  await page.getByLabel('Fixed address', { exact: true }).check();
  await page.locator('#addr').fill('192.168.1.60');
  await page.locator('#gw').fill('10.0.0.1');
  await next(page);
  await expect(page.getByText('The router must be in the network of the address.')).toBeVisible();
  await page.locator('#gw').fill('192.168.1.1');
  await shot(page, 'network');
  await next(page);

  // 5 Review: the button needs the computer name.
  await expect(current(page)).toHaveText('Review and erase');
  await expect(page.getByRole('table', { name: 'Summary' })).toContainText('fixed 192.168.1.60/24');
  await expect(page.getByText('These disks lose all their data.')).toBeVisible();
  const erase = page.getByRole('button', { name: 'Erase the disks and install' });
  await expect(erase).toBeDisabled();
  await page.getByLabel('Type the computer name').fill('home');
  await shot(page, 'review');
  await erase.click();

  // 6 Install: the progress, then the addresses.
  await expect(page.getByRole('heading', { name: 'Installing NidaOS' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'NidaOS is ready' })).toBeVisible({ timeout: 60_000 });
  await expect(page.getByRole('table', { name: 'Addresses' })).toContainText('https://home.local');
  await expect(page.getByRole('table', { name: 'Addresses' })).toContainText('ssh nida@home.local');
  await page.getByRole('button', { name: 'Show the details' }).click();
  await expect(page.getByLabel('Log')).toContainText('mkfs.xfs');
  await expect(page.getByLabel('Log')).not.toContainText('a long owner password');
  await shot(page, 'done');
});
