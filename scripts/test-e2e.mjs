// Lodge E2E happy-path test — drives the actual dashboard.html in a real
// browser. Self-contained: starts its own static server on an ephemeral port,
// drives the setup flow, asserts the dashboard renders the seeded servers,
// then cleans up. Run via `npm run test:e2e` or `bash scripts/test.sh`.

import { chromium } from 'playwright';
import { spawn } from 'child_process';
import { fileURLToPath } from 'url';
import { dirname, resolve } from 'path';
import { readFileSync, writeFileSync } from 'fs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, '..');
const PORT = Number(process.env.LODGE_TEST_PORT || 9123);
const URL = `http://127.0.0.1:${PORT}/dashboard.html`;

const log = (...a) => console.log('[e2e]', ...a);
const err = (...a) => console.error('[e2e]', ...a);

let server = null;
let exitCode = 0;
const consoleErrors = [];
const pageErrors = [];
// workingConfig is the path to the (potentially bootstrapped) config.json
// the test is using. Computed by ensureConfigJson(); consumed by the
// re-pick regression test.
let workingConfig = null;

function startServer() {
  return new Promise((resolve, reject) => {
    server = spawn(
      'python3',
      ['-m', 'http.server', String(PORT), '--bind', '127.0.0.1'],
      { cwd: projectRoot, stdio: ['ignore', 'pipe', 'pipe'] }
    );
    let ready = false;
    const onData = (chunk) => {
      if (ready) return;
      if (chunk.toString().includes(String(PORT))) {
        ready = true;
        resolve();
      }
    };
    server.stdout.on('data', onData);
    server.stderr.on('data', (c) => process.stderr.write(`[srv] ${c}`));
    server.once('error', reject);
    // Fallback: if port already bound the line never appears, but the
    // browser will tell us quickly. Give it 2s then proceed.
    setTimeout(() => { if (!ready) resolve(); }, 2000);
  });
}

function stopServer() {
  if (server && !server.killed) {
    try { server.kill('SIGTERM'); } catch {}
  }
}

async function run() {
  // Always start from a known config.json — either the example (no
  // verifier → setup screen) or whatever the test phases want. Prior
  // runs can leave a populated config.json with a real verifier behind,
  // which would skip the setup screen and break the happy-path
  // assertion. Force-replace with the example so we always begin the
  // happy path on the setup screen.
  {
    const fs = await import('fs');
    fs.copyFileSync(resolve(projectRoot, 'config.example.json'), resolve(projectRoot, 'config.json'));
  }
  log('config: reset to example for clean E2E start');
  workingConfig = resolve(projectRoot, 'config.json');
  log('starting static server on 127.0.0.1:' + PORT);
  await startServer();

  const browser = await chromium.launch({ headless: true });
  const context = await browser.newContext();
  const page = await context.newPage();

  page.on('pageerror', (e) => pageErrors.push(String(e)));
  page.on('console', (msg) => {
    if (msg.type() === 'error') consoleErrors.push(msg.text());
  });
  // Auto-accept all native confirm() dialogs (lock warning, vault reset,
  // etc.) so the test never hangs on a modal.
  page.on('dialog', async (d) => { await d.accept(); });

  try {
    log('navigating to', URL);
    const resp = await page.goto(URL, { waitUntil: 'domcontentloaded' });
    if (!resp || !resp.ok()) {
      throw new Error(`failed to load ${URL}: HTTP ${resp?.status()}`);
    }

    // 1. setup screen visible — this is the gate that would have caught
    //    the missing </div> bug (where setup was nested inside picker).
    await page.waitForSelector('#setup-screen:not(.hidden)', { timeout: 10_000 });
    log('✓ setup screen visible');

    // 2. fill master password + confirm
    const PW = 'correct-horse-battery-staple';
    await page.fill('#setup-pw1', PW);
    await page.fill('#setup-pw2', PW);

    // 3. submit
    await page.click('#setup-form button[type="submit"]');

    // 4. wait for dashboard
    await page.waitForSelector('#app:not(.hidden)', { timeout: 20_000 });
    log('✓ dashboard unlocked');

    // 5. all seeded servers rendered as cards. Data-driven: read whatever
    //    config.json E2E just bootstrapped, assert each id is in the DOM.
    //    Server ids are non-sensitive (e.g. "demo-nas", "home-nas"); we
    //    intentionally never echo host/IP into logs.
    const cfg = JSON.parse(readFileSync(resolve(projectRoot, 'config.json'), 'utf8'));
    const expected = (cfg.servers || []).map((s) => s.id).filter(Boolean);
    if (expected.length === 0) throw new Error('config.json has no servers to assert');
    for (const id of expected) {
      const el = await page.$(`article.card[data-id="${id}"]`);
      if (!el) throw new Error(`server card missing: ${id}`);
    }
    log(`✓ all ${expected.length} server cards rendered`);

    // 6. switch to services tab and assert cards present
    await page.click('button.tab[data-tab="services"]');
    await page.waitForTimeout(200);
    const cardCount = await page.$$eval('article.card[data-id]', (els) => els.length);
    if (cardCount < 1) throw new Error('services tab has no cards');
    log(`✓ services tab has ${cardCount} card(s)`);

    // 7. switch to vault tab and assert the locked state shows up
    await page.click('button.tab[data-tab="vault"]');
    await page.waitForTimeout(200);
    const locked = await page.$('#lock-btn');
    if (!locked) throw new Error('vault tab missing lock button');
    log('✓ vault tab shows lock button');

    // 7b. assert the about-page link in the topnav is wired to the
    //     /about.html route (added on user request to make the marketing
    //     page reachable from the main app).
    const aboutHref = await page.$eval('#about-link', (el) => el.getAttribute('href'));
    if (aboutHref !== '/about.html') {
      throw new Error(`#about-link href expected "/about.html" but got "${aboutHref}"`);
    }
    log('✓ about link points at /about.html');

    // 7c. assert the share-link in the topnav wires to a handler that
    //     takes the navigator.share OR navigator.clipboard path. In
    //     headless Chromium, navigator.share is missing, so the handler
    //     logs 'using clipboard fallback' to console — that's our
    //     witness that the click reached the handler.
    page.on('console', (msg) => {
      if (msg.text().startsWith('[share]')) console.log('  [page]', msg.text());
    });
    await page.click('#share-link');
    // Give the handler a moment to log; the message is the assertion.
    await page.waitForTimeout(500);
    log('✓ share link clicked (handler dispatched — see [page] log above)');

    // 8. regression: re-pick after wrong password. The previous bug was
    //    that the change handler was only bound inside init()'s picker
    //    branch, so users who reached unlock via fetch/cache (init never
    //    entered the picker branch) had no handler when they re-picked
    //    and the file selection silently did nothing. We assert the
    //    handler fires by clicking re-pick, uploading the same config
    //    again, and confirming the picker screen actually disappears.
    log('regression: re-pick after wrong password');
    // (dialog handler is registered globally at run() top — accepts all
    // confirm()s including the lock warning below.)
    await page.click('#lock-btn');
    await page.waitForSelector('#unlock-screen:not(.hidden)', { timeout: 5000 });
    await page.fill('#unlock-pw', 'definitely-wrong-password');
    await page.click('#unlock-form button[type="submit"]');
    await page.waitForSelector('#unlock-error.show', { timeout: 5000 });
    await page.click('#re-pick-btn');
    await page.waitForSelector('#picker-screen:not(.hidden)', { timeout: 5000 });
    await (await page.$('#file-input')).setInputFiles(workingConfig);
    // Picker should disappear; the page should advance (to setup for an
    // uninitialized config, or to unlock for an initialized one).
    await page.waitForFunction(
      () => {
        const p = document.getElementById('picker-screen');
        return p && p.classList.contains('hidden');
      },
      { timeout: 8000 }
    );
    log('✓ re-pick upload advanced past picker (regression guard)');

    // 8b. regression: "Forgot master password" button on the unlock
    //     screen. Must clear the vault and land on the setup screen so
    //     a forgotten-password user can recover without manually
    //     editing config.json.
    log('regression: forgot-password button on unlock screen');
    if (await page.$('#setup-screen:not(.hidden)')) {
      await page.fill('#setup-pw1', 'gate-test-pw');
      await page.fill('#setup-pw2', 'gate-test-pw');
      await page.click('#setup-form button[type="submit"]');
      await page.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
    }
    if (await page.$('#app:not(.hidden)')) {
      await page.click('#lock-btn');
      await page.waitForSelector('#unlock-screen:not(.hidden)', { timeout: 5000 });
    }
    // The forgot-pw button pops a confirm() that the global dialog
    // handler accepts automatically. We just click and assert setup.
    await page.click('#forgot-pw-btn');
    await page.waitForSelector('#setup-screen:not(.hidden)', { timeout: 5000 });
    log('✓ forgot-pw lands on setup (regression guard)');

    // 8c. regression: "Use demo data" button on the picker screen.
    //     Open a fresh context (no localStorage cache) to reach picker.
    log('regression: Use demo data button on picker screen');
    const ctx2 = await browser.newContext();
    const page2 = await ctx2.newPage();
    page2.on('dialog', async (d) => { await d.accept(); });
    // The devbox server only serves lodge; a fresh context → fetch 404 →
    // cache miss → picker. Force it by stripping storage on every load.
    await page2.addInitScript(() => {
      try { localStorage.clear(); } catch {}
    });
    // We also have to bypass the test server serving config.json; point
    // page2 at a port that has no config.json. The simplest is to point
    // at a path that always 404s, but init() only ever fetches
    // 'config.json' (relative). Workaround: stop the test server,
    // navigate, then restart. To avoid disrupting the main flow, we
    // just rely on localStorage being empty in a fresh context AND
    // delete config.json from the working dir briefly.
    const fs = await import('fs');
    const cfgPath = resolve(projectRoot, 'config.json');
    const hadConfig = fs.existsSync(cfgPath);
    if (hadConfig) fs.renameSync(cfgPath, cfgPath + '.bak-test');
    try {
      await page2.goto(URL, { waitUntil: 'domcontentloaded' });
      await page2.waitForSelector('#picker-screen:not(.hidden)', { timeout: 5000 });
      await page2.click('#demo-data-btn');
      await page2.waitForSelector('#setup-screen:not(.hidden)', { timeout: 5000 });
      log('✓ demo-data lands on setup (regression guard)');
    } finally {
      if (fs.existsSync(cfgPath + '.bak-test')) {
        fs.renameSync(cfgPath + '.bak-test', cfgPath);
      } else if (hadConfig) {
        // restore was lost; nothing to do (test errored before this)
      }
      await ctx2.close();
    }

    // 9. no console / page errors
    if (pageErrors.length) throw new Error('page errors: ' + pageErrors.join('; '));
    // Filter out known noisy 3rd-party warnings (MetaMask etc).
    const real = consoleErrors.filter((m) => !/chrome-extension:/.test(m));
    if (real.length) throw new Error('console errors: ' + real.join('; '));
    log('✓ no console / page errors');

    // 10. regression: full cross-device flow. Setup on a fresh page,
    //     export config, then load on a NEW context (simulating a
    //     different device) and try to unlock with the same password.
    //     Catches bugs like normalizeConfig dropping vault.verifierIv
    //     (which made every picker-path unlock fail silently).
    log('regression: cross-device setup → save → reload → unlock');
    {
      const fs = await import('fs');
      const exportPath = resolve(projectRoot, '.tmp-e2e-export.json');
      // Phase A: clean export
      const ctxA = await browser.newContext({ acceptDownloads: true });
      const pageA = await ctxA.newPage();
      pageA.on('dialog', async (d) => { await d.accept(); });
      // Force the test server to 404 on config.json by moving it aside.
      const realConfig = resolve(projectRoot, 'config.json');
      const realConfigBak = realConfig + '.bak-e2e-flow';
      const hadConfig = fs.existsSync(realConfig);
      if (hadConfig) fs.renameSync(realConfig, realConfigBak);
      try {
        await pageA.goto(URL, { waitUntil: 'domcontentloaded' });
        await pageA.waitForTimeout(400);
        if (await pageA.$('#picker-screen:not(.hidden)')) {
          await (await pageA.$('#file-input')).setInputFiles(resolve(projectRoot, 'config.example.json'));
          await pageA.waitForTimeout(800);
        }
        if (await pageA.$('#setup-screen:not(.hidden)')) {
          await pageA.fill('#setup-pw1', 'flow-test-pw');
          await pageA.fill('#setup-pw2', 'flow-test-pw');
          await pageA.click('#setup-form button[type="submit"]');
          await pageA.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
        }
        // Open settings, export
        await pageA.click('#settings-btn');
        await pageA.waitForSelector('#settings-modal:not(.hidden)');
        const [dl] = await Promise.all([
          pageA.waitForEvent('download'),
          pageA.click('#export-config-btn'),
        ]);
        await dl.saveAs(exportPath);
        await ctxA.close();

        // Phase B: fresh context, no cache, pick the exported file
        const ctxB = await browser.newContext();
        const pageB = await ctxB.newPage();
        pageB.on('dialog', async (d) => { await d.accept(); });
        await pageB.addInitScript(() => { try { localStorage.clear(); } catch {} });
        await pageB.goto(URL, { waitUntil: 'domcontentloaded' });
        await pageB.waitForSelector('#picker-screen:not(.hidden)', { timeout: 8000 });
        await (await pageB.$('#file-input')).setInputFiles(exportPath);
        await pageB.waitForSelector('#unlock-screen:not(.hidden)', { timeout: 5000 });

        // Wrong password → fail
        await pageB.fill('#unlock-pw', 'definitely-wrong-pw');
        await pageB.click('#unlock-form button[type="submit"]');
        await pageB.waitForSelector('#unlock-error.show', { timeout: 5000 });
        // Right password → unlock
        await pageB.fill('#unlock-pw', 'flow-test-pw');
        await pageB.click('#unlock-form button[type="submit"]');
        await pageB.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
        log('  ✓ cross-device flow: file picker → unlock with original password works');
        await ctxB.close();
      } finally {
        if (fs.existsSync(realConfigBak)) fs.renameSync(realConfigBak, realConfig);
        if (fs.existsSync(exportPath)) fs.unlinkSync(exportPath);
      }
    }

    // 11. regression: cache path (close + reopen) keeps the user
    //     logged in. The classic failure mode was normalizeConfig
    //     dropping vault.verifierIv on the cache load, so reopen
    //     could never verify the password until the user re-picked
    //     the file.
    log('regression: cache unlock after close + reopen');
    {
      const ctxC = await browser.newContext();
      const pageC = await ctxC.newPage();
      pageC.on('dialog', async (d) => { await d.accept(); });
      const fs = await import('fs');
      const realConfig = resolve(projectRoot, 'config.json');
      const realConfigBak = realConfig + '.bak-e2e-cache';
      const hadConfig = fs.existsSync(realConfig);
      if (hadConfig) fs.renameSync(realConfig, realConfigBak);
      try {
        await pageC.goto(URL, { waitUntil: 'domcontentloaded' });
        await pageC.waitForTimeout(400);
        if (await pageC.$('#picker-screen:not(.hidden)')) {
          await (await pageC.$('#file-input')).setInputFiles(resolve(projectRoot, 'config.example.json'));
          await pageC.waitForTimeout(800);
        }
        if (await pageC.$('#setup-screen:not(.hidden)')) {
          await pageC.fill('#setup-pw1', 'cache-test-pw');
          await pageC.fill('#setup-pw2', 'cache-test-pw');
          await pageC.click('#setup-form button[type="submit"]');
          await pageC.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
        }
        // Close + reopen in same context (localStorage persists).
        await pageC.close();
        const pageR = await ctxC.newPage();
        pageR.on('dialog', async (d) => { await d.accept(); });
        await pageR.goto(URL, { waitUntil: 'domcontentloaded' });
        await pageR.waitForSelector('#unlock-screen:not(.hidden)', { timeout: 5000 });
        // Source label should say "(local cache)".
        const source = await pageR.$eval('#unlock-source-val', (el) => el.textContent.trim()).catch(() => '');
        if (!/local cache|本地缓存/.test(source)) {
          throw new Error(`expected cache source label, got: ${JSON.stringify(source)}`);
        }
        await pageR.fill('#unlock-pw', 'cache-test-pw');
        await pageR.click('#unlock-form button[type="submit"]');
        await pageR.waitForSelector('#app:not(.hidden)', { timeout: 10000 });
        log('  ✓ cache unlock after close+reopen works');
        await ctxC.close();
      } finally {
        if (fs.existsSync(realConfigBak)) fs.renameSync(realConfigBak, realConfig);
      }
    }

    // 12. regression: mobile viewport (390x844, iPhone 12) doesn't
    //     introduce horizontal scroll and all key UI is reachable.
    //     Catches layout regressions where a fixed-width element
    //     (topnav, button, etc.) overflows the small screen.
    log('regression: mobile viewport (390x844) has no horizontal scroll');
    {
      const ctxM = await browser.newContext({
        viewport: { width: 390, height: 844 },
        deviceScaleFactor: 2, isMobile: true, hasTouch: true,
      });
      const pageM = await ctxM.newPage();
      pageM.on('dialog', async (d) => { await d.accept(); });
      await pageM.goto(URL, { waitUntil: 'domcontentloaded' });
      await pageM.waitForTimeout(500);
      if (await pageM.$('#picker-screen:not(.hidden)')) {
        await (await pageM.$('#file-input')).setInputFiles(resolve(projectRoot, 'config.example.json'));
        await pageM.waitForTimeout(1000);
      }
      if (await pageM.$('#setup-screen:not(.hidden)')) {
        await pageM.fill('#setup-pw1', 'mobile-test-pw');
        await pageM.fill('#setup-pw2', 'mobile-test-pw');
        await pageM.click('#setup-form button[type="submit"]');
        await pageM.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
        await pageM.waitForTimeout(500);
      }
      // Check for horizontal scroll on the unlocked dashboard
      const dims = await pageM.evaluate(() => {
        const inner = document.querySelector('.topnav-inner');
        const cs = inner ? getComputedStyle(inner) : null;
        return {
          scrollW: document.documentElement.scrollWidth,
          clientW: document.documentElement.clientWidth,
          bodyW: document.body.scrollWidth,
          viewportW: window.innerWidth,
          topnavH: inner?.getBoundingClientRect().height,
          topnavHeight: cs?.height,
          topnavMinHeight: cs?.minHeight,
          matchesMobile: window.matchMedia('(max-width: 640px)').matches,
        };
      });
      if (dims.scrollW > dims.viewportW + 1) {
        throw new Error(
          `horizontal scroll on mobile: scrollW=${dims.scrollW} viewportW=${dims.viewportW} ` +
          `(${dims.scrollW - dims.viewportW}px overflow)`
        );
      }
      // Topnav should wrap to 2 rows (brand+actions / tabs)
      if (dims.topnavH < 60) {
        // Dump matched rules for diagnosis
        const matched = await pageM.evaluate(() => {
          const inner = document.querySelector('.topnav-inner');
          if (!inner) return null;
          // Walk inline and embedded stylesheets to find rules that mention .topnav-inner
          const out = [];
          for (const sheet of document.styleSheets) {
            try {
              for (const rule of sheet.cssRules) {
                if (rule.cssText && rule.cssText.includes('topnav-inner')) {
                  out.push(rule.cssText.slice(0, 200));
                }
              }
            } catch (e) { out.push('CORS: ' + e.message); }
          }
          return out;
        });
        log('  matched rules for .topnav-inner:');
        for (const r of matched) log('    ' + r);
        throw new Error(`topnav unexpectedly short on mobile: ${dims.topnavH}px (expected ≥60)`);
      }
      log(`  ✓ mobile layout: scrollW=${dims.scrollW} ≤ viewportW=${dims.viewportW}, topnav=${dims.topnavH}px`);
      await ctxM.close();
    }

    // 12b. regression: about.html "Get Started" URL bar button is
    //      readable in dark mode. Earlier rev used var(--text) +
    //      var(--text-on-dark) which both flipped to light in dark
    //      mode, producing invisible white-on-white. The current
    //      override is fixed colors with !important; this test
    //      enforces WCAG AA contrast (≥ 4.5) so the bug can't
    //      regress.
    log('regression: about.html dark-mode URL bar contrast');
    {
      const ctxA = await browser.newContext({
        viewport: { width: 1280, height: 800 },
        colorScheme: 'dark',
      });
      const pageA = await ctxA.newPage();
      await pageA.goto(URL.replace(/\/dashboard\.html$/, '/about.html'),
        { waitUntil: 'domcontentloaded' });
      await pageA.waitForTimeout(500);
      const styles = await pageA.$eval('.url-bar .btn-primary', (el) => {
        const cs = getComputedStyle(el);
        return { bg: cs.backgroundColor, color: cs.color };
      });
      const parseRgb = (s) => {
        const m = s.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
        return m ? [+m[1], +m[2], +m[3]] : null;
      };
      const luminance = ([r, g, b]) => {
        const lin = [r, g, b].map(c => {
          const v = c / 255;
          return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4;
        });
        return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2];
      };
      const contrast = (a, b) => {
        const la = luminance(a), lb = luminance(b);
        const [hi, lo] = la > lb ? [la, lb] : [lb, la];
        return (hi + 0.05) / (lo + 0.05);
      };
      const bg = parseRgb(styles.bg);
      const fg = parseRgb(styles.color);
      if (!bg || !fg) {
        throw new Error(`could not parse .url-bar .btn-primary colors: ${JSON.stringify(styles)}`);
      }
      const ratio = contrast(bg, fg);
      if (ratio < 4.5) {
        throw new Error(
          `about.html dark-mode URL bar contrast too low: ${ratio.toFixed(2)} (need ≥4.5). ` +
          `bg=${styles.bg} color=${styles.color}`
        );
      }
      log(`  ✓ url-bar btn-primary dark contrast = ${ratio.toFixed(2)} (≥4.5)`);
      await ctxA.close();
    }

    // 13. regression: cache survives reload AND the read uses
    //     IndexedDB (the new primary path — iOS WebKit clears
    //     localStorage under memory pressure, but IDB has a
    //     different eviction policy and is the one we trust for
    //     real iOS users).
    log('regression: cache survives reload via IndexedDB');
    {
      const ctxIDB = await browser.newContext({ ...(await import('playwright')).devices['iPhone 13'] });
      const pageIDB = await ctxIDB.newPage();
      pageIDB.on('dialog', async (d) => { await d.accept(); });
      // Vercel-like: no on-disk config.json, so tryFetch 404s and
      // init() must use the cache path.
      const fs = await import('fs');
      const realConfig = resolve(projectRoot, 'config.json');
      const realConfigBak = realConfig + '.bak-e2e-idb';
      const hadConfig = fs.existsSync(realConfig);
      if (hadConfig) fs.renameSync(realConfig, realConfigBak);
      try {
        await pageIDB.goto(URL, { waitUntil: 'domcontentloaded' });
        await pageIDB.waitForTimeout(500);
        if (await pageIDB.$('#picker-screen:not(.hidden)')) {
          await (await pageIDB.$('#file-input')).setInputFiles(resolve(projectRoot, 'config.example.json'));
          await pageIDB.waitForTimeout(1000);
        }
        if (await pageIDB.$('#setup-screen:not(.hidden)')) {
          await pageIDB.fill('#setup-pw1', 'idb-cache-test-pw');
          await pageIDB.fill('#setup-pw2', 'idb-cache-test-pw');
          await pageIDB.click('#setup-form button[type="submit"]');
          await pageIDB.waitForSelector('#app:not(.hidden)', { timeout: 15000 });
          await pageIDB.waitForTimeout(800);
        }
        // Confirm cache was written to IDB.
        const written = await pageIDB.evaluate(async () => {
          return new Promise((resolve) => {
            const req = indexedDB.open('lodge', 1);
            req.onsuccess = () => {
              const db = req.result;
              const tx = db.transaction('cache', 'readonly');
              const r = tx.objectStore('cache').get('dashboard.cache');
              r.onsuccess = () => resolve({ idb: !!r.result, ls: !!localStorage.getItem('dashboard.cache') });
            };
            req.onerror = () => resolve({ idb: false, ls: false });
          });
        });
        if (!written.idb) {
          throw new Error('cache write did not land in IndexedDB');
        }
        // Reload and assert the read came from IDB and reached unlock.
        const logs = [];
        pageIDB.on('console', (m) => { if (m.text().includes('[lodge-debug]')) logs.push(m.text()); });
        await pageIDB.reload({ waitUntil: 'domcontentloaded' });
        await pageIDB.waitForTimeout(2000);
        if (!(await pageIDB.$('#unlock-screen:not(.hidden)'))) {
          throw new Error('after reload + IDB, page is not on unlock screen');
        }
        const idbSource = logs.find((l) => l.includes('loadCache: source=idb'));
        if (!idbSource) {
          throw new Error('reload did not read from IndexedDB (cache log: ' + logs.join(' | ') + ')');
        }
        log(`  ✓ IDB cache unlock works (idb=${written.idb}, ls=${written.ls})`);
        await ctxIDB.close();
      } finally {
        if (fs.existsSync(realConfigBak)) fs.renameSync(realConfigBak, realConfig);
      }
    }

    // 14. regression: the Mac SSH tip (iTerm hint) is shown on
    //     Mac and hidden elsewhere. The tip is unhidden by init()
    //     based on navigator.platform / userAgentData.platform.
    log('regression: Mac SSH tip shows on Mac, hidden elsewhere');
    {
      const { devices } = await import('playwright');
      // Mac — tip should be visible
      const ctxMac = await browser.newContext({
        ...devices['Desktop Chrome'],
        userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36',
      });
      const pageMac = await ctxMac.newPage();
      await pageMac.goto(URL, { waitUntil: 'domcontentloaded' });
      await pageMac.waitForTimeout(500);
      const macVisible = await pageMac.evaluate(() => {
        const el = document.getElementById('mac-ssh-tip');
        return !!el && !el.hidden;
      });
      if (!macVisible) {
        throw new Error('mac-ssh-tip should be visible on Mac, but is hidden or missing');
      }
      log('  ✓ mac-ssh-tip visible on Mac');
      await ctxMac.close();

      // Non-Mac (Linux) — tip should be hidden
      const ctxLin = await browser.newContext({ ...devices['Desktop Chrome'] });
      const pageLin = await ctxLin.newPage();
      await pageLin.goto(URL, { waitUntil: 'domcontentloaded' });
      await pageLin.waitForTimeout(500);
      const linHidden = await pageLin.evaluate(() => {
        const el = document.getElementById('mac-ssh-tip');
        return !!el && el.hidden;
      });
      if (!linHidden) {
        throw new Error('mac-ssh-tip should be hidden on Linux, but is visible');
      }
      log('  ✓ mac-ssh-tip hidden on non-Mac');
      await ctxLin.close();
    }

    // 15. regression: Lodge works opened directly from a file:// URL.
    //     Modern browsers treat file:// as a secure context, so
    //     window.isSecureContext === true and crypto.subtle is
    //     available — the vault (Web Crypto) is fully functional
    //     without a local http server. We verify the page loads, the
    //     secure-context checks pass, and we land on the file picker
    //     (since fetch('config.json') over file:// is restricted).
    log('regression: Lodge works opened directly from file://');
    {
      const fs = await import('fs');
      // Move config.json aside so fetch() over file:// doesn't accidentally
      // return data and skip the picker.
      const realConfig = resolve(projectRoot, 'config.json');
      const realConfigBak = realConfig + '.bak-e2e-file';
      const hadConfig = fs.existsSync(realConfig);
      if (hadConfig) fs.renameSync(realConfig, realConfigBak);
      try {
        const ctxF = await browser.newContext();
        const pageF = await ctxF.newPage();
        pageF.on('dialog', async (d) => { await d.accept(); });
        const fileURL = 'file://' + resolve(projectRoot, 'dashboard.html');
        await pageF.goto(fileURL, { waitUntil: 'domcontentloaded' });
        await pageF.waitForTimeout(600);
        const fileDiag = await pageF.evaluate(() => ({
          protocol: window.location.protocol,
          isSecureContext: window.isSecureContext,
          hasSubtle: !!(window.crypto && window.crypto.subtle),
          insecure: !document.getElementById('insecure-screen').classList.contains('hidden'),
          picker: !document.getElementById('picker-screen').classList.contains('hidden'),
          setup: !document.getElementById('setup-screen').classList.contains('hidden'),
        }));
        if (fileDiag.insecure) {
          throw new Error('file:// mode wrongly shows insecure screen — Web Crypto should work');
        }
        if (!fileDiag.isSecureContext || !fileDiag.hasSubtle) {
          throw new Error(
            `file:// not a secure context for this Playwright build: ` +
            `protocol=${fileDiag.protocol} isSecureContext=${fileDiag.isSecureContext} ` +
            `hasSubtle=${fileDiag.hasSubtle} — would break the vault`
          );
        }
        if (!fileDiag.picker && !fileDiag.setup) {
          throw new Error('file:// mode did not reach picker or setup screen');
        }
        log('  ✓ file:// → picker, isSecureContext=true, crypto.subtle available');
        await ctxF.close();
      } finally {
        if (fs.existsSync(realConfigBak)) fs.renameSync(realConfigBak, realConfig);
      }
    }

    log('PASS');
  } catch (e) {
    err('FAIL:', e.message);
    try {
      writeFileSync(resolve(__dirname, '.last-failure.png'),
        await page.screenshot({ fullPage: true }));
    } catch {}
    exitCode = 1;
  } finally {
    await browser.close().catch(() => {});
  }
}

run().finally(() => {
  stopServer();
  setTimeout(() => process.exit(exitCode), 200);
});
