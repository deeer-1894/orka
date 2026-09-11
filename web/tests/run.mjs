import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';
import { spawnSync } from 'node:child_process';
const root = fileURLToPath(new URL('../', import.meta.url));
const output = mkdtempSync(join(root, '.test-build-'));
try {
  const built = spawnSync(process.execPath, ['node_modules/typescript/bin/tsc', '--target', 'ES2022', '--module', 'commonjs', '--moduleResolution', 'node', '--strict', '--skipLibCheck', '--outDir', output, 'src/lib/runRecovery.ts', 'src/lib/sessionFiles.ts'], { cwd: root, stdio: 'inherit' });
  if (built.status !== 0) process.exitCode = built.status ?? 1;
  else {
    writeFileSync(join(output, 'package.json'), '{"type":"commonjs"}');
    const run = spawnSync(process.execPath, ['--test', 'tests/recovery.test.mjs', 'tests/sessionFiles.test.mjs'], { cwd: root, stdio: 'inherit', env: { ...process.env, ORKA_TEST_BUILD: output } });
    process.exitCode = run.status ?? 1;
  }
} finally { rmSync(output, { recursive: true, force: true }); }
