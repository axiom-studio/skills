import { spawn } from 'node:child_process';

export const BROWSER_VIDEO_MIME = 'video/webm; codecs="vp8"';
export const BROWSER_VIDEO_SIZE = Object.freeze({ width: 1280, height: 800 });

// A separate local display per browser prevents capturing another session.
// No network listener, recordings, screenshots or user-selected command paths.
export async function createBrowserDisplay({ signal, spawnProcess = spawn } = {}) {
  if (signal?.aborted) throw new Error('Browser display is unavailable');
  const child = spawnProcess('Xvfb', ['-displayfd', '3', '-screen', '0', '1280x800x24',
    '-nolisten', 'tcp', '-noreset'], { stdio: ['ignore', 'ignore', 'ignore', 'pipe'] });
  let closed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    signal?.removeEventListener('abort', close);
    child.kill('SIGTERM');
  };
  signal?.addEventListener('abort', close, { once: true });
  try {
    const display = await new Promise((resolve, reject) => {
      let value = '';
      const timer = setTimeout(() => finish(), 5000);
      const failed = () => finish();
      const finish = result => {
        clearTimeout(timer);
        child.off('error', failed);
        child.off('exit', failed);
        child.stdio[3].off('data', data);
        signal?.removeEventListener('abort', failed);
        result ? resolve(result) : reject(new Error('Browser display is unavailable'));
      };
      const data = chunk => {
        value += chunk.toString('ascii');
        if (value.length > 8 || !/^[0-9]*\n?$/.test(value)) return finish();
        if (value.endsWith('\n')) {
          const number = Number(value.trim());
          finish(Number.isInteger(number) && number >= 0 && number <= 65535 ? `:${number}` : undefined);
        }
      };
      child.once('error', failed);
      child.once('exit', failed);
      child.stdio[3].on('data', data);
      signal?.addEventListener('abort', failed, { once: true });
    });
    child.on('error', close);
    return { display, close, process: child };
  } catch { close(); throw new Error('Browser display is unavailable'); }
}

// Called only after the transport has claimed and checked an exclusive human
// lease. The transport must abort this signal on disconnect/revocation/return.
// Async iteration preserves pipe backpressure instead of accumulating frames.
export function openBrowserVideo({ display, signal, spawnProcess = spawn }) {
  if (!/^:[0-9]{1,5}$/.test(display) || !signal || signal.aborted) throw new Error('Browser video is unavailable');
  const child = spawnProcess('ffmpeg', ['-nostdin', '-hide_banner', '-loglevel', 'error',
    '-f', 'x11grab', '-draw_mouse', '1', '-framerate', '20', '-video_size', '1280x800',
    '-i', `${display}.0`, '-an', '-c:v', 'libvpx', '-deadline', 'realtime', '-cpu-used', '8',
    '-threads', '2', '-b:v', '1800k', '-g', '20', '-lag-in-frames', '0', '-pix_fmt', 'yuv420p',
    '-f', 'webm', '-live', '1', '-cluster_time_limit', '100', '-flush_packets', '1', 'pipe:1'],
  { stdio: ['ignore', 'pipe', 'ignore'] });
  let closed = false;
  let failed = false;
  const close = () => {
    if (closed) return;
    closed = true;
    signal.removeEventListener('abort', close);
    child.stdout.destroy();
    child.kill('SIGTERM');
  };
  child.on('error', () => { failed = true; close(); });
  signal.addEventListener('abort', close, { once: true });
  return {
    mimeType: BROWSER_VIDEO_MIME, ...BROWSER_VIDEO_SIZE, close,
    async *[Symbol.asyncIterator]() {
      try {
        for await (const chunk of child.stdout) {
          if (closed || signal.aborted) break;
          yield chunk;
        }
        if ((!closed || failed) && !signal.aborted) throw new Error();
      } catch {
        if (!signal.aborted) throw new Error('Browser video is unavailable');
      } finally { close(); }
    },
  };
}
