import { spawn } from 'node:child_process';
import { BROWSER_VIDEO_SIZE } from './browser-video.mjs';

const KEYS = Object.freeze({ Enter: 'Return', Tab: 'Tab', 'Shift+Tab': 'shift+Tab',
  Backspace: 'BackSpace', Delete: 'Delete', Escape: 'Escape', ArrowLeft: 'Left',
  ArrowRight: 'Right', ArrowUp: 'Up', ArrowDown: 'Down', Home: 'Home', End: 'End',
  PageUp: 'Prior', PageDown: 'Next', 'ControlOrMeta+A': 'ctrl+a' });
const fields = (input, allowed) => input && typeof input === 'object' && !Array.isArray(input) &&
  Object.keys(input).every(key => allowed.includes(key));

// Human-only desktop input on the same isolated display as the video encoder.
// Text travels over stdin, never process arguments, a shell, logs, or the model.
export class BrowserDesktopInput {
  #display; #control; #spawn; #signal;
  constructor({ display, control, signal, spawnProcess = spawn }) {
    if (!/^:[0-9]{1,5}$/.test(display) || !control || !signal) throw new Error('Browser input is unavailable');
    this.#display = display; this.#control = control; this.#spawn = spawnProcess; this.#signal = signal;
  }
  async #run(args, text = '') {
    if (this.#signal.aborted) throw new Error();
    await new Promise((resolve, reject) => {
      const child = this.#spawn('xdotool', args, { env: { ...process.env, DISPLAY: this.#display },
        stdio: ['pipe', 'ignore', 'ignore'] });
      let settled = false;
      const finish = ok => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        this.#signal.removeEventListener('abort', abort);
        if (!ok) child.kill('SIGKILL');
        ok ? resolve() : reject(new Error('Browser input is unavailable'));
      };
      const abort = () => finish(false);
      const timer = setTimeout(abort, 2000);
      this.#signal.addEventListener('abort', abort, { once: true });
      child.once('error', abort);
      child.once('exit', code => finish(code === 0));
      child.stdin.on('error', abort);
      child.stdin.end(text);
      if (this.#signal.aborted) abort();
    });
  }
  async release() { await this.#run(['mouseup', '1', 'mouseup', '2', 'mouseup', '3']); }
  async dispatch(principal, leaseID, input) {
    try {
      return await this.#control.human(principal, leaseID, async () => {
        let args, text;
        switch (input?.type) {
          case 'pointer': {
            if (!fields(input, ['type', 'action', 'x', 'y', 'button']) ||
              !['move', 'down', 'up'].includes(input.action) || !Number.isInteger(input.x) ||
              !Number.isInteger(input.y) || input.x < 0 || input.y < 0 ||
              input.x >= BROWSER_VIDEO_SIZE.width || input.y >= BROWSER_VIDEO_SIZE.height ||
              ![0, 1, 2].includes(input.button)) throw new Error();
            args = ['mousemove', String(input.x), String(input.y)];
            if (input.action !== 'move') args.push(input.action === 'down' ? 'mousedown' : 'mouseup',
              String([1, 2, 3][input.button]));
            break;
          }
          case 'text':
            if (!fields(input, ['type', 'text']) || typeof input.text !== 'string' ||
              !input.text.length || input.text.length > 4096 || /[\u0000-\u001f\u007f]/.test(input.text)) throw new Error();
            args = ['type', '--clearmodifiers', '--delay', '0', '--file', '-']; text = input.text;
            break;
          case 'key':
            if (!fields(input, ['type', 'key']) || !Object.hasOwn(KEYS, input.key)) throw new Error();
            args = ['key', '--clearmodifiers', KEYS[input.key]];
            break;
          case 'scroll':
            if (!fields(input, ['type', 'deltaY']) || !Number.isInteger(input.deltaY) ||
              !input.deltaY || Math.abs(input.deltaY) > 1200) throw new Error();
            args = ['click', '--repeat', String(Math.min(10, Math.ceil(Math.abs(input.deltaY) / 100))),
              '--delay', '0', input.deltaY > 0 ? '5' : '4'];
            break;
          default: throw new Error();
        }
        await this.#run(args, text);
        return { type: 'ack' };
      });
    } catch { throw new Error('Browser control request could not be completed'); }
  }
}
