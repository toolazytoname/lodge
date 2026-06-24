#!/usr/bin/env node
// Focused verification of the inline "copy IP" button on the server card.
// Loads dashboard.html in a real browser, grants clipboard permission,
// clicks the new .card-row-copy button on a server, and asserts:
//   1. The button is present next to the address row.
//   2. Clicking it writes the host to the clipboard.
//   3. The button visually flips to a checkmark and reverts.
//
// Run: node scripts/verify-copy-host.mjs

import { chromium } from 'playwright';
import { spawn } from 'child_process';
import { resolve, dirname } from 'path';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, '..');
const PORT = 9125;
const URL = `http://127.0.0.1:${PORT}/dashboard.html`;

const log = (...a) => console.log('[copy-host]', ...a);
const err = (...a) => console.error('[copy-host]', ...a);

function startStaticServer() {
  return new Promise((resolveP, rejectP) => {
    const srv = spawn('python3', ['-m', 'http.server', String(PORT)], {
      cwd: projectRoot, stdio: ['ignore', 'pipe', 'pipe'],
    });
    let ready = false;
    srv.stdout.on('data', (chunk) => {
      if (!ready && /Serving HTTP/.test(chunk.toString())) {
        ready = true; resolveP(srv);
      }
    });
    srv.on('error', rejectP);
    setTimeout(() => { if (!ready) resolveP(srv); }, 1500);
  });
}

let server;
let exitCode = 0;
const cleanup = () => { if (server) server.kill('SIGTERM'); process.exit(exitCode); };

(async () => {
  server = await startStaticServer();
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] });
  const page = await ctx.newPage();
  page.on('pageerror', (e) => err('pageerror:', e.message));
  page.on('console', (m) => { if (m.type() === 'error') err('console.error:', m.text()); });

  try {
    // Use demo data so we get a blank in-memory config; then go through
    // setup so the app unlocks, and add a server via the modal.
    // Block any /config.json fetch + clear storage so the picker is shown.
    await page.route('**/config.json', (r) => r.abort());
    await page.goto(URL);
    await page.evaluate(() => { try { localStorage.clear(); } catch {} });
    await page.goto(URL);
    await page.waitForLoadState('domcontentloaded');
    await page.locator('#demo-data-btn').click();

    // Demo data has no vault verifier, so we land on setup.
    await page.waitForSelector('#setup-screen:not(.hidden)', { timeout: 5000 });
    const PW = 'verify-copy-host-pw-1';
    await page.fill('#setup-pw1', PW);
    await page.fill('#setup-pw2', PW);
    await page.click('#setup-form button[type="submit"]');
    await page.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
    log('✓ dashboard unlocked with demo config');

    // Add a server via the modal so the address row exists.
    await page.locator('#add-server-btn').click();
    await page.waitForSelector('#server-modal:not(.hidden)', { timeout: 5000 });
    await page.fill('#server-alias', 'verify-host');
    await page.fill('#server-host', '192.0.2.42');
    await page.fill('#server-port', '2222');
    await page.fill('#server-user', 'verify');
    await page.click('#server-form button[type="submit"]');
    await page.waitForFunction(() => document.getElementById('server-modal').classList.contains('hidden'), { timeout: 5000 });
    log('✓ server "verify-host" added');

    // Now the address row + copy button should be present.
    await page.locator('.card-row-copy').first().waitFor({ timeout: 5000 });

    const cardCount = await page.locator('.card-row-copy').count();
    if (cardCount < 1) throw new Error(`expected at least 1 .card-row-copy, got ${cardCount}`);
    log(`✓ found ${cardCount} inline copy button(s)`);

    // Verify the copy button sits inside the address row of a server card.
    const inCardRow = await page.locator('.card-row .card-row-copy').count();
    if (inCardRow < 1) throw new Error('copy button is not inside a .card-row');
    log(`✓ ${inCardRow} copy button(s) inside an address row`);

    // Read the host that the first card displays, then click its copy button
    // and check the clipboard matches. The button copies just the IP/host
    // (not the port), so compare against the data-copy-value attribute.
    const firstCard = page.locator('article.card').first();
    const displayed = (await firstCard.locator('.card-row .value').first().textContent() || '').trim();
    const expected = await firstCard.locator('.card-row-copy').getAttribute('data-copy-value');
    if (!displayed || !expected) throw new Error('could not read host from first card');
    log(`displayed: "${displayed}", copy value: "${expected}"`);

    await firstCard.locator('.card-row-copy').click();
    // Give the toast a moment to render so we can also assert the feedback.
    await page.waitForTimeout(150);

    const clip = await page.evaluate(() => navigator.clipboard.readText());
    if (clip !== expected) throw new Error(`clipboard mismatch: got "${clip}", expected "${expected}"`);
    log(`✓ clipboard now contains host`);

    const isCopied = await firstCard.locator('.card-row-copy').evaluate((el) => el.classList.contains('copied'));
    if (!isCopied) throw new Error('copy button did not get .copied state class');
    log('✓ copy button shows .copied state');

    // Toast should show a "Copied" message (en or zh). The setup flow
    // also left a "Master password set" toast in the queue, so wait
    // for one that contains the copy wording specifically.
    await page.locator('#toast-container .toast', { hasText: /copied|已复制/i }).first().waitFor({ timeout: 3000 });
    const toastText = await page.locator('#toast-container .toast', { hasText: /copied|已复制/i }).first().textContent();
    log(`✓ toast: "${(toastText || '').trim()}"`);

    // After ~1.2s the check should revert to the copy icon.
    await page.waitForTimeout(1400);
    const stillCopied = await firstCard.locator('.card-row-copy').evaluate((el) => el.classList.contains('copied'));
    if (stillCopied) throw new Error('copy button did not revert from .copied state');
    log('✓ copy button reverted to default state');

    // Regression: clicking SSH should NOT surface a toast. The toast was
    // a "command + Copy" fallback that the user explicitly removed —
    // the deep-link handoff is now the only feedback. Wait for any
    // prior toasts to clear, then click and assert no new toast.
    await page.waitForTimeout(2000); // let prior toasts auto-dismiss
    const toastsBefore = await page.locator('#toast-container .toast').count();
    await firstCard.locator('button[data-action="ssh"]').click();
    await page.waitForTimeout(500);
    const toastsAfter = await page.locator('#toast-container .toast').count();
    if (toastsAfter > toastsBefore) {
      const txt = await page.locator('#toast-container .toast').last().textContent();
      throw new Error(`SSH click surfaced a toast: "${(txt || '').trim()}"`);
    }
    log('✓ SSH click does not show a toast');

    log('ALL CHECKS PASSED');
    // Take a screenshot of the servers tab with the new copy button visible.
    await page.screenshot({ path: 'verify-copy-host.png', fullPage: true });
    log('screenshot: verify-copy-host.png');
  } catch (e) {
    err('FAIL:', e.message);
    exitCode = 1;
  } finally {
    await browser.close();
    cleanup();
  }
})();
