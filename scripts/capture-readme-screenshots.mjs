// scripts/capture-readme-screenshots.mjs
//
// Captures English-locale screenshots of Lodge for the README.
//
// Why: the existing screenshots in the repo are Chinese-locale and
// can't be used in an English README. This script:
//   1. Pre-sets the UI language to English via localStorage
//   2. Intercepts the auto-fetched config.json and serves a sanitized
//      demo config (no real user data)
//   3. Walks through setup → dashboard, taking screenshots at each step
//
// Output: assets/screenshots/{setup,dashboard-servers,dashboard-services,dashboard-dark}.png

import { chromium } from 'playwright';
import { mkdirSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = join(__dirname, '..');
const OUT_DIR = join(ROOT, 'assets', 'screenshots');
mkdirSync(OUT_DIR, { recursive: true });

const URL_BASE = process.env.LODGE_URL || 'http://localhost:9011';
const MASTER_PW = 'demo-readme-password-2026';

// Sanitized demo config — no real IPs, no real tokens, mirrors
// the structure of config.example.json but with enough content to
// render a representative dashboard.
const DEMO_CONFIG = {
  version: 1,
  meta: { created: '2025-01-01T00:00:00.000Z', lastModified: '2025-01-01T00:00:00.000Z' },
  servers: [
    {
      id: 'nas',
      alias: 'Home NAS',
      host: '192.0.2.10',
      sshPort: 22,
      sshUser: 'root',
      tags: ['home', 'storage'],
      notes: 'FreeBSD, 4 bays',
    },
    {
      id: 'vps',
      alias: 'Hetzner VPS',
      host: '192.0.2.20',
      sshPort: 22,
      sshUser: 'deploy',
      tags: ['public'],
      notes: 'Nuremberg, CX22',
    },
    {
      id: 'rpi',
      alias: 'Pi-hole',
      host: '192.0.2.30',
      sshPort: 22,
      sshUser: 'pi',
      tags: ['home', 'network'],
      notes: 'Raspberry Pi 4',
    },
  ],
  services: [
    { id: 'jellyfin', name: 'Jellyfin', url: 'http://192.0.2.10:8096', type: 'media', icon: 'J', serverId: 'nas', description: 'Movies & TV' },
    { id: 'syncthing', name: 'Syncthing', url: 'http://192.0.2.10:8384', type: 'admin', icon: 'S', serverId: 'nas', description: 'File sync' },
    { id: 'pihole', name: 'Pi-hole', url: 'http://192.0.2.30/admin', type: 'admin', icon: 'P', serverId: 'rpi', description: 'DNS sinkhole' },
    { id: 'gitea', name: 'Gitea', url: 'http://192.0.2.20:3000', type: 'web', icon: 'G', serverId: 'vps', description: 'Git hosting' },
    { id: 'nextcloud', name: 'Nextcloud', url: 'http://192.0.2.10:8443', type: 'web', icon: 'N', serverId: 'nas', description: 'Files & calendar' },
  ],
  serviceTypes: ['media', 'admin', 'web', 'database', 'monitoring', 'other'],
  vault: {
    kdf: 'PBKDF2-SHA256',
    iterations: 600000,
    salt: '',
    verifier: '',
    verifierIv: '',
    items: [],
  },
  settings: { autoLockMinutes: 5, theme: 'auto', clearClipboardSeconds: 30, servicesGroupBy: 'server' },
};

async function capture() {
  const browser = await chromium.launch();
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2, // 2x for crisp README images
  });

  // Intercept the auto-fetched config.json so we never touch the
  // user's real data on disk.
  await context.route('**/config.json', (route) => {
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(DEMO_CONFIG),
    });
  });

  const page = await context.newPage();

  // Pre-set language BEFORE the page script runs. applyLang() will
  // then re-translate every data-i18n element to English.
  await page.addInitScript(() => {
    localStorage.setItem('lodge.lang', 'en');
  });

  // ---- 1. Load page ----
  await page.goto(`${URL_BASE}/dashboard.html`, { waitUntil: 'domcontentloaded' });

  // Wait for applyLang to run and the secure-context check to settle.
  // If Web Crypto fails we'd land on the insecure-context screen —
  // http://localhost is a secure context, so this should always pass.
  await page.waitForFunction(
    () => {
      const setup = document.getElementById('setup-screen');
      const picker = document.getElementById('picker-screen');
      const app = document.getElementById('app');
      return (setup && !setup.classList.contains('hidden')) ||
             (picker && !picker.classList.contains('hidden')) ||
             (app && !app.classList.contains('hidden'));
    },
    { timeout: 8000 },
  );

  // ---- 2. Setup master password ----
  // config.json had empty vault salt → proceedWithConfig shows setup-screen
  const onSetup = await page.locator('#setup-screen:not(.hidden)').count();
  if (onSetup) {
    await page.locator('#setup-pw1').fill(MASTER_PW);
    await page.locator('#setup-pw2').fill(MASTER_PW);
    await page.screenshot({ path: join(OUT_DIR, 'setup.png'), fullPage: false });
    await page.locator('#setup-form button[type="submit"]').click();
  } else {
    console.warn('Did not land on setup screen; current state:', await page.evaluate(() => ({
      setup: !!document.querySelector('#setup-screen:not(.hidden)'),
      unlock: !!document.querySelector('#unlock-screen:not(.hidden)'),
      picker: !!document.querySelector('#picker-screen:not(.hidden)'),
      app: !!document.querySelector('#app:not(.hidden)'),
    })));
  }

  // ---- 3. Wait for dashboard ----
  await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
  // Wait for at least one server card to render
  await page.waitForSelector('#servers-grid .card', { timeout: 8000 });
  // Give animations / fonts a beat to settle
  await page.waitForTimeout(400);

  await page.screenshot({ path: join(OUT_DIR, 'dashboard-servers.png'), fullPage: false });

  // ---- 4. Switch to Services tab for a second screenshot ----
  const servicesTab = page.locator('button.tab[data-tab="services"]').first();
  if (await servicesTab.count()) {
    await servicesTab.click();
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT_DIR, 'dashboard-services.png'), fullPage: false });
  }

  // ---- 5. Optional: dark mode screenshot for theme showcase ----
  await page.evaluate(() => {
    const root = document.documentElement;
    root.dataset.theme = 'dark';
  });
  await page.waitForTimeout(200);
  await page.screenshot({ path: join(OUT_DIR, 'dashboard-dark.png'), fullPage: false });

  await browser.close();
  console.log('Wrote screenshots to', OUT_DIR);
}

capture().catch((err) => {
  console.error(err);
  process.exit(1);
});