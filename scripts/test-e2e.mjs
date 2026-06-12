// Lodge E2E happy-path test — drives the actual dashboard.html in a real
// browser. Self-contained: starts its own static server on an ephemeral port,
// drives the setup flow, asserts the dashboard renders the seeded servers,
// then cleans up. Run via `npm run test:e2e` or `bash scripts/test.sh`.

import { chromium } from 'playwright';
import { spawn } from 'child_process';
import { fileURLToPath } from 'url';
import { dirname, resolve } from 'path';
import { copyFileSync, existsSync, readFileSync, writeFileSync } from 'fs';

const __dirname = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(__dirname, '..');
const PORT = Number(process.env.LODGE_TEST_PORT || 9123);
const URL = `http://127.0.0.1:${PORT}/dashboard.html`;

// Bootstrap config.json for the test. We never commit config.json (it may
// hold real server IPs). Precedence:
//   1. config.json         — already in working dir, use it
//   2. config.local.json   — user's real data (gitignored), copy to config.json
//   3. config.example.json — public demo, copy to config.json
//                            (this is what a fresh clone gets)
function ensureConfigJson() {
  const target = resolve(projectRoot, 'config.json');
  if (existsSync(target)) return 'existing';
  for (const src of ['config.local.json', 'config.example.json']) {
    const from = resolve(projectRoot, src);
    if (existsSync(from)) {
      copyFileSync(from, target);
      return `bootstrapped from ${src}`;
    }
  }
  throw new Error('no config.json and no config.example.json to bootstrap from');
}

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
  log('config:', ensureConfigJson());
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
