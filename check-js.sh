#!/usr/bin/env node
// Validate the inline <script> block in dashboard.html parses as ES2022.
// Replaces a hard-coded path that only worked inside one dev container.
//
// Usage:
//   node check-js.sh                 # validate ./dashboard.html
//   node check-js.sh path/to/file.html
//
// Pulls acorn on demand via npx — no local install needed.

const fs = require('fs');
const path = require('path');
const { execSync } = require('child_process');

const target = path.resolve(process.argv[2] || 'dashboard.html');
if (!fs.existsSync(target)) {
  console.error(`ERR: file not found: ${target}`);
  process.exit(2);
}

let acorn;
try {
  acorn = require('acorn');
} catch {
  console.error('acorn not installed locally; fetching via npx…');
  const tmp = fs.mkdtempSync(path.join(require('os').tmpdir(), 'acorn-'));
  execSync(`npm --prefix "${tmp}" install acorn@8 --silent --no-audit --no-fund`, { stdio: 'inherit' });
  acorn = require(path.join(tmp, 'node_modules', 'acorn'));
}

const html = fs.readFileSync(target, 'utf8');
const start = html.indexOf('<script>');
if (start < 0) {
  console.error('ERR: no <script> tag found');
  process.exit(1);
}
const end = html.lastIndexOf('</script>');
const code = html.substring(start + '<script>'.length, end);

try {
  acorn.parse(code, { ecmaVersion: 2022, sourceType: 'script' });
  console.log('OK');
  process.exit(0);
} catch (e) {
  console.log('ERR:', e.message);
  console.log('  at line', e.loc && e.loc.line);
  process.exit(1);
}
