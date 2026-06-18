#!/usr/bin/env node
// Focused verification of the user-editable service types +
// groupBy toggle + type chip features. Runs independently of the
// full e2e suite so it can confirm the feature works even when
// unrelated (e.g. iOS/IndexedDB) tests flake.
//
// Usage: node scripts/verify-types-groupby.mjs

import { chromium } from 'playwright';
import { spawn } from 'child_process';
import { resolve, dirname } from 'path';
import { existsSync } from 'fs';
import { fileURLToPath } from 'url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, '..');
const PORT = 9125;
const URL = `http://127.0.0.1:${PORT}/dashboard.html`;

const log = (...a) => console.log('[types-verify]', ...a);
const err = (...a) => console.error('[types-verify]', ...a);

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
  const realConfigBak = realConfig + '.bak-types-verify';
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
      await page.fill('#setup-pw1', 'types-verify-pw');
      await page.fill('#setup-pw2', 'types-verify-pw');
      await page.click('#setup-form button[type="submit"]');
      await page.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
    }

    // --- Feature A: type chip on the existing demo service ---
    await page.click('button.tab[data-tab="services"]');
    await page.waitForTimeout(300);

    const chipTexts = await page.$$eval('a.service-card .service-type-chip', (els) =>
      els.map((e) => e.textContent.trim())
    );
    log('chips on demo cards:', chipTexts);
    if (!chipTexts.includes('1panel')) {
      throw new Error('expected chip text "1panel" on demo card');
    }

    // --- Feature B: add a known type via the form ---
    await page.click('#add-service-btn');
    await page.waitForSelector('#service-modal:not(.hidden)', { timeout: 3000 });
    await page.fill('#service-name', 'Jellyfin');
    await page.evaluate(() => {
      document.getElementById('service-form').noValidate = true;
    });
    await page.fill('#service-url', 'http://192.0.2.99:8096');
    await page.selectOption('#service-type', 'web');
    await page.click('#service-form button[type="submit"]');
    await page.waitForFunction(
      () => document.getElementById('service-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    const allChips = await page.$$eval('a.service-card .service-type-chip', (els) =>
      els.map((e) => e.textContent.trim())
    );
    log('chips after add:', allChips);
    if (!allChips.includes('Web')) {
      throw new Error('expected chip "Web" after adding service, got: ' + JSON.stringify(allChips));
    }

    // --- Feature C: GroupBy toggle ---
    await page.click('[data-action="group-by"][data-value="type"]');
    await page.waitForTimeout(150);
    const headingsByType = await page.$$eval('#services-container .section-title', (els) =>
      els.map((e) => e.textContent.replace(/\s*·\s*\d+\s*$/, '').trim())
    );
    log('headings when grouped by type:', headingsByType);
    if (!headingsByType.includes('1panel')) {
      throw new Error('groupBy=type missing "1panel" heading');
    }

    // Switch to "none" — headings should disappear.
    await page.click('[data-action="group-by"][data-value="none"]');
    await page.waitForTimeout(150);
    const headingsByNone = await page.$$eval('#services-container .section-title', (els) => els.length);
    log('headings count when grouped by none:', headingsByNone);
    if (headingsByNone !== 0) {
      throw new Error('groupBy=none should produce 0 headings, got: ' + headingsByNone);
    }

    // Switch back to "server" — original behavior.
    await page.click('[data-action="group-by"][data-value="server"]');
    await page.waitForTimeout(150);
    const headingsByServer = await page.$$eval('#services-container .section-title', (els) =>
      els.map((e) => e.textContent.replace(/\s*·\s*\d+\s*$/, '').trim())
    );
    log('headings when grouped by server:', headingsByServer);
    if (!headingsByServer.includes('Other')) {
      throw new Error('groupBy=server should include "Other" bucket, got: ' + JSON.stringify(headingsByServer));
    }

    // --- Feature D: Manage-types modal ---
    await page.click('#settings-btn');
    await page.waitForSelector('#settings-modal:not(.hidden)', { timeout: 3000 });
    await page.click('#manage-types-btn');
    await page.waitForSelector('#types-modal:not(.hidden)', { timeout: 3000 });

    // Confirm seeded types are shown.
    const initialRows = await page.$$eval('#types-list .types-row-input', (els) =>
      els.map((e) => e.value)
    );
    log('initial type rows:', initialRows);
    if (initialRows.length !== 7) {
      throw new Error('expected 7 initial type rows, got: ' + initialRows.length);
    }

    // Add a new type.
    await page.click('#types-add-btn');
    const inputs = await page.$$('#types-list .types-row-input');
    await inputs[inputs.length - 1].fill('monitoring');
    await page.click('#types-save-btn');
    await page.waitForFunction(
      () => document.getElementById('types-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    // Close the parent settings modal too — types-save only closes
    // the sub-modal, leaving settings open. That's intentional UX
    // (user might want to do more in settings) but we need to
    // dismiss it before clicking #add-service-btn.
    await page.click('#settings-modal [data-close]');
    await page.waitForFunction(
      () => document.getElementById('settings-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    // Verify the new type appears in the service-type dropdown.
    await page.click('#add-service-btn');
    await page.waitForSelector('#service-modal:not(.hidden)', { timeout: 3000 });
    const typeOptions = await page.$$eval('#service-type option', (els) =>
      els.map((o) => o.value)
    );
    log('type options after add:', typeOptions);
    if (!typeOptions.includes('monitoring')) {
      throw new Error('custom type "monitoring" not in dropdown: ' + JSON.stringify(typeOptions));
    }
    await page.click('#service-modal [data-close]');
    await page.waitForFunction(
      () => document.getElementById('service-modal').classList.contains('hidden'),
      { timeout: 3000 }
    );

    log('PASS — type chip, GroupBy toggle, and editable types all work');
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
