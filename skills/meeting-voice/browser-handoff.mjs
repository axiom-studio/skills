import { BrowserControl } from './browser-control.mjs';
import { BrowserHumanView } from './browser-human-view.mjs';
import { browserIntervention } from './browser-intervention.mjs';

// Private worker integration. Only a host-authorized transport may call handle;
// none of these commands belong in Execute or the model's action catalog.
export class BrowserHandoff {
  #scope;
  #pending;
  #onState;
  #timeoutMs;
  #videoFactory;
  #inputFactory;
  #desktopFactory;

  constructor({ tenantID, agentID, onState = () => {}, timeoutMs = 300000, videoFactory, inputFactory, desktopFactory }) {
    if (!tenantID || !agentID || !Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > 600000) {
      throw new Error('Invalid browser handoff configuration');
    }
    this.#scope = { tenantID, agentID };
    this.#onState = onState;
    this.#timeoutMs = timeoutMs;
    this.#videoFactory = videoFactory;
    this.#inputFactory = inputFactory;
    this.#desktopFactory = desktopFactory;
  }

  async request({ page, context, signal, reason = 'manual_confirmation' }) {
    if (this.#pending || signal?.aborted) throw new Error('Browser handoff is unavailable');
    const intervention = browserIntervention(reason);
    let resolve, reject;
    const completed = new Promise((ok, fail) => { resolve = ok; reject = fail; });
    // A status callback can fail before the await below is reached. Keep its
    // cleanup rejection handled without changing the caller's failure result.
    void completed.catch(() => {});
    const pending = { resolve, reject, expiresAt: Date.now() + this.#timeoutMs };
    const control = new BrowserControl({ ...this.#scope, close: async () => {
      pending.video?.abort();
      reject(new Error('Browser handoff ended without returning control'));
      await context.close();
    } });
    pending.control = control;
    pending.view = this.#inputFactory ? this.#inputFactory({ control, signal }) : new BrowserHumanView({ page, control });
    this.#pending = pending;
    const cancel = () => { void control.close().catch(() => {}); };
    // The deadline includes time waiting for someone to claim control.
    const timer = setTimeout(cancel, this.#timeoutMs);
    timer.unref?.();
    signal?.addEventListener('abort', cancel, { once: true });
    page.on?.('close', cancel);
    try {
      await this.#onState('awaiting_user', intervention);
      await completed;
      if (signal?.aborted || control.state !== 'automation') throw new Error('Browser handoff was cancelled');
      await this.#onState('joining');
    } catch {
      await control.close().catch(() => {});
      throw new Error('Browser handoff did not complete');
    } finally {
      pending.video?.abort();
      clearTimeout(timer);
      signal?.removeEventListener('abort', cancel);
      page.off?.('close', cancel);
      this.#pending = undefined;
    }
  }

  // Video is not an input command and never holds the serialized input lock
  // while waiting for an encoder or a network consumer. Every yielded chunk
  // still checks the exclusive human lease before leaving the worker.
  async *stream(principal, leaseID, { signal, desktop = false } = {}) {
    const pending = this.#pending;
    const factory = desktop ? this.#desktopFactory : this.#videoFactory;
    if (!pending || !factory || signal?.aborted) throw new Error('Browser video is unavailable');
    const controller = new AbortController();
    let video;
    const abort = () => controller.abort();
    try {
      await pending.control.human(principal, leaseID, async () => {
        if (pending.video) throw new Error();
        pending.video = controller;
        video = await factory({ signal: controller.signal });
        if (desktop) pending.desktop = video;
      });
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) controller.abort();
      for await (const chunk of video) {
        if (controller.signal.aborted) break;
        await pending.control.human(principal, leaseID, () => {});
        if (controller.signal.aborted) break;
        yield chunk;
      }
    } catch { throw new Error('Browser video is unavailable'); }
    finally {
      signal?.removeEventListener('abort', abort);
      controller.abort();
      video?.close();
      if (pending.desktop === video) pending.desktop = undefined;
      if (pending.video === controller) pending.video = undefined;
    }
  }

  async writeDesktop(principal, leaseID, bytes) {
    const pending = this.#pending;
    if (!pending?.desktop) throw new Error('Desktop unavailable');
    await pending.control.human(principal, leaseID, () => pending.desktop.write(bytes));
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
        await pending.control.human(principal, command.leaseID, async () => {
          await pending.view.release?.();
          pending.returning = true;
          await pending.desktop?.close();
          pending.video?.abort();
        });
        await pending.control.returnControl(principal, command.leaseID);
        pending.resolve();
        return { type: 'ack' };
      }
      if (command.type === 'cancel') {
        // Stopping the native stream is part of explicit return, not an
        // ambiguous disconnect. Its IPC cleanup must not race that return.
        if (pending.returning) throw new Error();
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
