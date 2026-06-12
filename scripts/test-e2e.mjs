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
    // Auto-accept the lock confirm() if the test has unsaved changes.
    const dialogPromise = page.waitForEvent('dialog', { timeout: 1000 }).then(d => d.accept()).catch(() => {});
    await page.click('#lock-btn');
    await dialogPromise;
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

    // 9. no console / page errors
    if (pageErrors.length) throw new Error('page errors: ' + pageErrors.join('; '));
    // Filter out known noisy 3rd-party warnings (MetaMask etc).
    const real = consoleErrors.filter((m) => !/chrome-extension:/.test(m));
    if (real.length) throw new Error('console errors: ' + real.join('; '));
    log('✓ no console / page errors');

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
