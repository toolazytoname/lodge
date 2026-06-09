const fs = require('fs');
const acorn = require('/tmp/node_modules/acorn');
const html = fs.readFileSync('/root/opencode-app/lodge/dashboard.html', 'utf8');
const start = html.indexOf('<script>') + 8;
const end = html.lastIndexOf('</script>');
const code = html.substring(start, end);
try {
  acorn.parse(code, { ecmaVersion: 2022, sourceType: 'script' });
  console.log('OK');
  process.exit(0);
} catch (e) {
  console.log('ERR:', e.message);
  console.log('  at line', e.loc?.line);
  process.exit(1);
}
