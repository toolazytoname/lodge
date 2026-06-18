#!/usr/bin/env node
// Focused verification of the NBSP-prefixed service URL sanitization fix.
// Runs independently of the full e2e suite so it can confirm the fix
// even when unrelated (e.g. iOS/IndexedDB) tests flake.
//
// Usage: node scripts/verify-nbsp-fix.mjs

import { chromium } from 'playwright';
import { spawn } from 'child_process';
import { resolve, dirname } from 'path';
import { existsSync } from 'fs';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, '..');
const PORT = 9124;
const URL = `http://127.0.0.1:${PORT}/dashboard.html`;

const log = (...a) => console.log('[nbsp-verify]', ...a);
const err = (...a) => console.error('[nbsp-verify]', ...a);

function startStaticServer() {
  return new Promise((resolveP, rejectP) => {
    const srv = spawn('python3', ['-m', 'http.server', String(PORT)], {
      cwd: projectRoot,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    let ready = false;
    srv.stdout.on('data', (chunk) => {
      const s = chunk.toString();
      if (!ready && /Serving HTTP/.test(s)) {
        ready = true;
        resolveP(srv);
      }
    });
    srv.stderr.on('data', () => {});
    srv.on('error', rejectP);
    setTimeout(() => { if (!ready) resolveP(srv); }, 1500);
  });
}

async function main() {
  // Back up any real config.json so the test starts clean.
  const fs = await import('fs');
  const realConfig = resolve(projectRoot, 'config.json');
  const realConfigBak = realConfig + '.bak-nbsp-verify';
  const hadConfig = existsSync(realConfig);
  if (hadConfig) fs.renameSync(realConfig, realConfigBak);

  const srv = await startStaticServer();
  let browser;
  try {
    browser = await chromium.launch();
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    page.on('dialog', async (d) => { await d.accept(); });
    page.on('pageerror', (e) => console.error('[page-error]', e.message));

    log('navigate', URL);
    await page.goto(URL, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(400);

    if (await page.$('#picker-screen:not(.hidden)')) {
      await (await page.$('#file-input')).setInputFiles(resolve(projectRoot, 'config.example.json'));
      await page.waitForTimeout(800);
    }
    if (await page.$('#setup-screen:not(.hidden)')) {
      await page.fill('#setup-pw1', 'nbsp-verify-pw');
      await page.fill('#setup-pw2', 'nbsp-verify-pw');
      await page.click('#setup-form button[type="submit"]');
      await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
    }

    // --- Layer 1+4: form submit sanitizes, render shows clean href ---
    await page.click('button.tab[data-tab="services"]');
    await page.waitForTimeout(300);
    await page.click('#add-service-btn');
    await page.waitForSelector('#service-modal:not(.hidden)', { timeout: 3000 });

    await page.fill('#service-name', 'NBSP repro');
    await page.evaluate(() => {
      document.getElementById('service-url').value = ' http://192.168.1.50:9999';
      // NBSP-prefixed URLs fail the browser's native type="url"
      // validation, which would block the form from submitting at
      // all. Turn it off so we can exercise the sanitization path.
      document.getElementById('service-form').noValidate = true;
    });

    const inputBefore = await page.$eval('#service-url', (el) => ({
      value: el.value,
      firstChar: el.value.charCodeAt(0),
    }));
    log('input before submit:', inputBefore);
    if (inputBefore.firstChar !== 0xA0) {
      throw new Error('Test setup failed — input does not start with U+00A0 (NBSP). got charCode=' + inputBefore.firstChar);
    }

    await page.click('#service-form button[type="submit"]');
    await page.waitForFunction(
      () => document.getElementById('service-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    const cardHref = await page.$eval('a.service-card', (el) => el.getAttribute('href'));
    log('rendered card href:', JSON.stringify(cardHref));
    if (cardHref !== 'http://192.168.1.50:9999') {
      throw new Error('FAIL: rendered href is dirty: ' + JSON.stringify(cardHref));
    }

    const cardUrlText = await page.$eval('.service-card-url', (el) => el.textContent);
    log('rendered card url text:', JSON.stringify(cardUrlText));
    if (cardUrlText !== 'http://192.168.1.50:9999') {
      throw new Error('FAIL: visible url text is dirty: ' + JSON.stringify(cardUrlText));
    }

    // --- Layer 3: modal prefill sanitizes ---
    await page.click('button[data-action="edit-service"]');
    await page.waitForSelector('#service-modal:not(.hidden)', { timeout: 3000 });
    const inputAfter = await page.$eval('#service-url', (el) => ({
      value: el.value,
      firstChar: el.value.charCodeAt(0),
    }));
    log('Edit modal input:', inputAfter);
    if (inputAfter.value !== 'http://192.168.1.50:9999' || inputAfter.firstChar === 0xA0) {
      throw new Error('FAIL: Edit modal shows dirty URL: ' + JSON.stringify(inputAfter));
    }

    // --- Layer 2: normalizeConfig heal on poisoned cache reload ---
    log('verifying normalizeConfig heal on poisoned cache reload…');
    await page.click('#service-modal button[data-close]');
    await page.waitForFunction(
      () => document.getElementById('service-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    // Grab the live cache from localStorage, poison services[0], wipe IDB.
    // IDB is checked before localStorage on load, so we must clear it
    // for our poisoned localStorage entry to be the one read on reload.
    const poisonedUrl = await page.evaluate(async () => {
      const raw = localStorage.getItem('dashboard.cache');
      if (!raw) return null;
      const obj = JSON.parse(raw);
      const svcs = obj?.config?.services || [];
      if (!svcs.length) return null;
      svcs[0].url = ' http://203.0.113.5:7777';
      obj.config.services = svcs;
      localStorage.setItem('dashboard.cache', JSON.stringify(obj));
      try { indexedDB.deleteDatabase('lodge'); } catch {}
      return svcs[0].url;
    });
    log('seeded poisoned URL in localStorage:', JSON.stringify(poisonedUrl));
    if (poisonedUrl === null) {
      throw new Error('no localStorage cache entry found to poison');
    }

    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(800);

    // Unlock with the password we set up earlier.
    if (await page.$('#unlock-screen:not(.hidden)')) {
      await page.fill('#unlock-pw', 'nbsp-verify-pw');
      await page.click('#unlock-form button[type="submit"]');
      await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
    } else if (await page.$('#setup-screen:not(.hidden)')) {
      await page.fill('#setup-pw1', 'nbsp-verify-pw');
      await page.fill('#setup-pw2', 'nbsp-verify-pw');
      await page.click('#setup-form button[type="submit"]');
      await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
    } else if (await page.$('#picker-screen:not(.hidden)')) {
      throw new Error('app did not pick up the seeded cache — picker is showing');
    }

    await page.click('button.tab[data-tab="services"]');
    await page.waitForTimeout(300);
    const reloadedHrefs = await page.$$eval('a.service-card', (els) => els.map((e) => e.getAttribute('href')));
    log('all card hrefs after poisoned-cache reload:', reloadedHrefs);
    if (!reloadedHrefs.length) throw new Error('no service cards rendered after reload');
    for (const h of reloadedHrefs) {
      if (h && h.charCodeAt(0) === 0xA0) {
        throw new Error('FAIL: cache-healed card still has leading NBSP: ' + JSON.stringify(h));
      }
    }
    // The poisoned URL (with leading NBSP) was written into services[0]
    // and should have been healed to exactly 'http://203.0.113.5:7777'.
    const cleaned = reloadedHrefs.find((h) => h === 'http://203.0.113.5:7777');
    if (!cleaned) {
      throw new Error('FAIL: poisoned URL was not healed — expected http://203.0.113.5:7777 in rendered hrefs, got: ' + JSON.stringify(reloadedHrefs));
    }

    log('PASS — NBSP-prefixed service URLs are sanitized on save, on re-edit, and healed from poisoned cache');
  } catch (e) {
    err('FAIL:', e.message);
    process.exitCode = 1;
  } finally {
    if (browser) await browser.close();
    if (hadConfig && existsSync(realConfigBak)) fs.renameSync(realConfigBak, realConfig);
    srv.kill();
  }
}

main();
