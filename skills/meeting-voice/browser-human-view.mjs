// Human-only page adapter. This must never be registered in the model tool
// catalog. Its caller authenticates the user; BrowserControl checks ownership
// and the exclusive lease again before touching the page. Do not log inputs,
// returned frames, or underlying browser errors.
const KEYS = new Set(['Enter', 'Tab', 'Shift+Tab', 'Backspace', 'Delete', 'Escape',
  'ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End', 'ControlOrMeta+A']);

function exactFields(value, fields) {
  return value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).every(key => fields.includes(key));
}

export class BrowserHumanView {
  #page;
  #control;

  constructor({ page, control }) {
    if (!page || !control) throw new Error('Browser page and control are required');
    this.#page = page;
    this.#control = control;
  }

  async dispatch(principal, leaseID, input) {
    // Normalize *all* failures, including Playwright errors that may quote
    // credential text typed by the human. Do not attach an error cause.
    try {
      return await this.#control.human(principal, leaseID, async () => {
        const viewport = this.#page.viewportSize();
        if (!viewport || viewport.width > 1920 || viewport.height > 1200) throw new Error();
        switch (input?.type) {
          case 'frame': {
            if (!exactFields(input, ['type'])) throw new Error();
            const bytes = await this.#page.screenshot({ type: 'jpeg', quality: 70,
              fullPage: false, timeout: 5000 });
            if (bytes.length > 2 * 1024 * 1024) throw new Error();
            return { type: 'frame', mimeType: 'image/jpeg', width: viewport.width,
              height: viewport.height, bytes };
          }
          case 'click':
            if (!exactFields(input, ['type', 'x', 'y']) || !Number.isInteger(input.x) ||
              !Number.isInteger(input.y) || input.x < 0 || input.y < 0 ||
              input.x >= viewport.width || input.y >= viewport.height) throw new Error();
            await this.#page.mouse.click(input.x, input.y);
            break;
          case 'text':
            if (!exactFields(input, ['type', 'text']) || typeof input.text !== 'string' ||
              !input.text.length || input.text.length > 4096 || /[\u0000-\u001f\u007f]/.test(input.text)) throw new Error();
            // This is direct user input into the focused browser control, not
            // a prompt, stored credential, clipboard, or agent instruction.
            await this.#page.keyboard.insertText(input.text);
            break;
          case 'key':
            if (!exactFields(input, ['type', 'key']) || !KEYS.has(input.key)) throw new Error();
            await this.#page.keyboard.press(input.key);
            break;
          case 'scroll':
            if (!exactFields(input, ['type', 'deltaY']) || !Number.isInteger(input.deltaY) ||
              Math.abs(input.deltaY) > 1200) throw new Error();
            await this.#page.mouse.wheel(0, input.deltaY);
            break;
          default: throw new Error();
        }
        return { type: 'ack' };
      });
    } catch {
      throw new Error('Browser control request could not be completed');
    }
  }
}
