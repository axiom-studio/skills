import { BrowserControl } from './browser-control.mjs';
import { BrowserHumanView } from './browser-human-view.mjs';

// Private worker integration. Only a host-authorized transport may call handle;
// none of these commands belong in Execute or the model's action catalog.
export class BrowserHandoff {
  #scope;
  #pending;
  #onState;
  #timeoutMs;

  constructor({ tenantID, agentID, onState = () => {}, timeoutMs = 300000 }) {
    if (!tenantID || !agentID || !Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 600000) {
      throw new Error('Invalid browser handoff configuration');
    }
    this.#scope = { tenantID, agentID };
    this.#onState = onState;
    this.#timeoutMs = timeoutMs;
  }

  async request({ page, context, signal }) {
    if (this.#pending || signal?.aborted) throw new Error('Browser handoff is unavailable');
    let resolve, reject;
    const completed = new Promise((ok, fail) => { resolve = ok; reject = fail; });
    // A status callback can fail before the await below is reached. Keep its
    // cleanup rejection handled without changing the caller's failure result.
    void completed.catch(() => {});
    const pending = { resolve, reject, expiresAt: Date.now() + this.#timeoutMs };
    const control = new BrowserControl({ ...this.#scope, close: async () => {
      reject(new Error('Browser handoff ended without returning control'));
      await context.close();
    } });
    pending.control = control;
    pending.view = new BrowserHumanView({ page, control });
    this.#pending = pending;
    const cancel = () => { void control.close().catch(() => {}); };
    // The deadline includes time waiting for someone to claim control.
    const timer = setTimeout(cancel, this.#timeoutMs);
    timer.unref?.();
    signal?.addEventListener('abort', cancel, { once: true });
    page.on?.('close', cancel);
    try {
      await this.#onState('awaiting_user');
      await completed;
      if (signal?.aborted || control.state !== 'automation') throw new Error('Browser handoff was cancelled');
      await this.#onState('joining');
    } catch {
      await control.close().catch(() => {});
      throw new Error('Browser handoff did not complete');
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener('abort', cancel);
      page.off?.('close', cancel);
      this.#pending = undefined;
    }
  }

  async handle(principal, command) {
    try {
      const pending = this.#pending;
      if (!pending || !command || typeof command !== 'object' || Array.isArray(command)) throw new Error();
      const allowed = command.type === 'claim' ? ['type'] : ['type', 'leaseID', 'input'];
      if (Object.keys(command).some(key => !allowed.includes(key))) throw new Error();
      if (command.type === 'claim') {
        const remaining = pending.expiresAt - Date.now();
        if (remaining < 1000) throw new Error();
        return await pending.control.takeOver(principal, remaining);
      }
      if (typeof command.leaseID !== 'string') throw new Error();
      if (command.type === 'input') return await pending.view.dispatch(principal, command.leaseID, command.input);
      if (command.input !== undefined) throw new Error();
      if (command.type === 'resume') {
        await pending.control.returnControl(principal, command.leaseID);
        pending.resolve();
        return { type: 'ack' };
      }
      if (command.type === 'cancel') {
        // Check ownership before closing; an unrelated user cannot terminate
        // another user's sign-in attempt.
        await pending.control.human(principal, command.leaseID, () => {});
        await pending.control.close();
        return { type: 'ack' };
      }
      throw new Error();
    } catch {
      throw new Error('Browser control request could not be completed');
    }
  }
}
