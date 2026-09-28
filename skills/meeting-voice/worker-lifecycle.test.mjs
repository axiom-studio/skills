import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { test } from 'node:test';

for (const failed of [false, true]) {
  test(`worker exits with IPC listeners after ${failed ? 'failure' : 'completion'}`, async () => {
    const moduleURL = new URL('./worker-lifecycle.mjs', import.meta.url).href;
    const child = spawn(process.execPath, ['--input-type=module', '-e', `
      import { runWorker } from ${JSON.stringify(moduleURL)};
      process.on('message', () => {});
      await runWorker(async () => { ${failed ? 'throw new Error("secret-provider-body")' : ''} });
    `], { stdio: ['ignore', 'pipe', 'pipe', 'ipc'] });
    let diagnostic = '';
    child.stderr.on('data', chunk => { diagnostic += chunk; });
    const timeout = setTimeout(() => child.kill('SIGKILL'), 3000);
    try {
      const [code, signal] = await once(child, 'exit');
      assert.equal(signal, null, 'worker required forced termination');
      assert.equal(code, failed ? 1 : 0);
      assert.doesNotMatch(diagnostic, /secret-provider-body/);
    } finally {
      clearTimeout(timeout);
    }
  });
}
