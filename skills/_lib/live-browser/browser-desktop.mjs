import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { setTimeout as delay } from 'node:timers/promises';
import { createBrowserDisplay } from './browser-video.mjs';

const execute = promisify(execFile);

// Xvfb alone provides pixels but no window activation/focus policy. A private
// window manager is required for native input and authentication popup windows.
export async function createBrowserDesktop({ signal }) {
  const display = await createBrowserDisplay({ signal });
  const manager = spawn('openbox', ['--sm-disable'], {
    env: { ...process.env, DISPLAY: display.display }, stdio: 'ignore',
  });
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    signal.removeEventListener('abort', close);
    manager.kill('SIGTERM');
    display.close();
  };
  manager.once('error', close);
  manager.once('exit', close);
  display.process.once('exit', close);
  signal.addEventListener('abort', close, { once: true });
  try {
    const deadline = Date.now() + 5000;
    while (!closed && !signal.aborted && Date.now() < deadline) {
      const result = await execute('xprop', ['-display', display.display, '-root', '_NET_SUPPORTING_WM_CHECK'],
        { signal, timeout: 1000, maxBuffer: 1024 }).catch(() => undefined);
      if (result && /window id # 0x[0-9a-f]+/i.test(result.stdout)) return { display: display.display, close };
      await delay(50, undefined, { signal });
    }
    throw new Error();
  } catch { close(); throw new Error('Browser desktop is unavailable'); }
}
