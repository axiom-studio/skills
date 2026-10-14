import { spawn, execFile } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { promisify } from 'node:util';
import { setTimeout as delay } from 'node:timers/promises';
import { createBrowserDisplay } from './browser-video.mjs';

const execute = promisify(execFile);

// The viewer only ever sees the browser, so windows get no title bar or
// borders and fill the display. A config file replaces openbox's defaults
// entirely, so click-to-focus (needed to switch to authentication popups) is
// declared here too.
const OPENBOX_CONFIG = `<?xml version="1.0" encoding="UTF-8"?>
<openbox_config xmlns="http://openbox.org/3.4/rc">
  <focus><focusNew>yes</focusNew><followMouse>no</followMouse></focus>
  <mouse>
    <context name="Client">
      <mousebind button="Left" action="Press"><action name="Focus"/><action name="Raise"/></mousebind>
      <mousebind button="Middle" action="Press"><action name="Focus"/><action name="Raise"/></mousebind>
      <mousebind button="Right" action="Press"><action name="Focus"/><action name="Raise"/></mousebind>
    </context>
  </mouse>
  <applications>
    <application class="*"><decor>no</decor><maximized>yes</maximized></application>
  </applications>
</openbox_config>
`;

// Xvfb alone provides pixels but no window activation/focus policy. A private
// window manager is required for native input and authentication popup windows.
export async function createBrowserDesktop({ signal }) {
  const display = await createBrowserDisplay({ signal });
  const configDir = mkdtempSync(join(tmpdir(), 'live-browser-openbox-'));
  const configFile = join(configDir, 'rc.xml');
  writeFileSync(configFile, OPENBOX_CONFIG);
  const manager = spawn('openbox', ['--sm-disable', '--config-file', configFile], {
    env: { ...process.env, DISPLAY: display.display }, stdio: 'ignore',
  });
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    signal.removeEventListener('abort', close);
    manager.kill('SIGTERM');
    display.close();
    rmSync(configDir, { recursive: true, force: true });
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
