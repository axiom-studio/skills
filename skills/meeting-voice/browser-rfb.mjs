import { spawn } from 'node:child_process';
import { connect } from 'node:net';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { setTimeout as delay } from 'node:timers/promises';

// No TCP listener, credentials, clipboard, file transfer or diagnostic output.
// The owner is the existing exclusive human lease, not a VNC password.
export async function openBrowserRFB({ display, signal }) {
  if (!/^:[0-9]{1,5}$/.test(display) || signal?.aborted) throw new Error('Desktop unavailable');
  const directory = await mkdtemp(join(tmpdir(), 'private-rfb-'));
  const path = join(directory, 'desktop');
  const child = spawn('x11vnc', ['-display', display, '-unixsock', path, '-rfbport', '0',
    '-no6', '-safer', '-nocmds', '-nosel', '-nosetclipboard', '-nosetprimary',
    '-nevershared', '-once', '-nopw', '-quiet', '-noxdamage', '-wait', '10', '-defer', '5'],
  { stdio: 'ignore' });
  let socket, closed = false, killTimer;
  const close = () => {
    if (closed) return;
    closed = true; socket?.destroy(); child.kill('SIGTERM');
    // X11 teardown can leave x11vnc waiting inside Xlib; do not retain a
    // private desktop process indefinitely after the lease is gone.
    killTimer = setTimeout(() => child.kill('SIGKILL'), 1000);
    killTimer.unref();
    signal?.removeEventListener('abort', close);
  };
  child.once('error', close);
  child.once('exit', () => { close(); clearTimeout(killTimer); void rm(directory, { recursive: true, force: true }); });
  signal?.addEventListener('abort', close, { once: true });
  try {
    for (let attempt = 0; attempt < 100 && !closed; attempt++) {
      socket = await new Promise(resolve => {
        const candidate = connect(path);
        candidate.once('connect', () => resolve(candidate));
        candidate.once('error', () => { candidate.destroy(); resolve(undefined); });
      });
      if (socket) break;
      await delay(25);
    }
    if (!socket || closed || signal?.aborted) throw new Error();
    socket.on('error', close);
    return {
      close,
      async write(bytes) {
        if (closed || !Buffer.isBuffer(bytes) || !bytes.length || bytes.length > 65536) throw new Error('Desktop unavailable');
        await new Promise((resolve, reject) => {
          const timer = setTimeout(() => { close(); reject(new Error('Desktop unavailable')); }, 3000);
          socket.write(bytes, error => { clearTimeout(timer); error ? reject(new Error('Desktop unavailable')) : resolve(); });
        });
      },
      async *[Symbol.asyncIterator]() {
        try { for await (const bytes of socket) yield bytes; }
        finally { close(); }
      },
    };
  } catch { close(); await rm(directory, { recursive: true, force: true }); throw new Error('Desktop unavailable'); }
}
