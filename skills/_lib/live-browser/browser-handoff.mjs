import { BrowserControl } from './browser-control.mjs';
import { browserIntervention } from './browser-intervention.mjs';

const LEASE_EXPIRED = 'Your browser control ended. Take control again to continue, then hand back to the agent.';

export class BrowserPausedError extends Error {
  constructor(status) { super('Browser automation is paused'); this.name = 'BrowserPausedError'; this.status = status; }
}

// Private host-only handoff for one live browser. Only a host-authorized
// transport may call handle/stream/watch/writeDesktop; none of these commands
// belong in Execute or the model's action catalog.
//
// Unlike a one-shot sign-in handoff, this lives as long as the browser:
// - a human may claim while the agent is automating (after the in-flight
//   action) or while the agent awaits the user;
// - a lease-free, view-only `watch` stream is available in every live state;
// - lease expiry pauses the agent (awaiting_user) instead of closing the page;
// - only the lease owner's explicit `resume` lets automation continue.
export class BrowserHandoff {
  #scope;
  #control;
  #intervention;
  #view;
  #videoFactory;
  #desktopFactory;
  #onState;
  #leaseMs;
  #maxWatchers;
  #watchers = new Set();
  #leased;
  #desktop;
  #returning = false;

  constructor({ tenantID, agentID, close, videoFactory, desktopFactory, inputFactory, onState = () => {},
    leaseMs = 300000, maxWatchers = 2, now }) {
    if (!tenantID || !agentID || typeof close !== 'function' || typeof inputFactory !== 'function' ||
      !Number.isInteger(leaseMs) || leaseMs < 1000 || leaseMs > 600000 ||
      !Number.isInteger(maxWatchers) || maxWatchers < 0 || maxWatchers > 8) {
      throw new Error('Invalid browser handoff configuration');
    }
    this.#scope = { tenantID, agentID };
    this.#videoFactory = videoFactory;
    this.#desktopFactory = desktopFactory;
    this.#onState = onState;
    this.#leaseMs = leaseMs;
    this.#maxWatchers = maxWatchers;
    this.#control = new BrowserControl({ ...this.#scope, onExpire: 'pause', now, onPause: () => this.#notify(), close: async () => {
      this.#intervention = undefined;
      this.#leased?.abort();
      for (const watcher of this.#watchers) watcher.abort();
      await close();
      this.#notify();
    } });
    this.#view = inputFactory({ control: this.#control });
  }

  get status() {
    const state = this.#control.state;
    if (state === 'closed') return 'none';
    if (state === 'automation') return 'automating';
    if (state === 'paused') return 'awaiting_user';
    return 'human';
  }

  get intervention() {
    if (this.status !== 'awaiting_user') return undefined;
    return this.#intervention ?? browserIntervention('other', LEASE_EXPIRED);
  }

  get lease() { return this.#control.lease; }

  #notify() {
    try { void Promise.resolve(this.#onState(this.status, this.intervention)).catch(() => {}); } catch { /* status is advisory */ }
  }

  // Model actions. Automation is serialized with human input; a paused or
  // human-held browser rejects with BrowserPausedError and never acts.
  async automate(operation) {
    try { return await this.#control.automate(operation); }
    catch (error) {
      if (this.#control.state !== 'automation' && error?.message === 'Browser automation is paused') {
        throw new BrowserPausedError(this.status);
      }
      throw error;
    }
  }

  settled(timeoutMs) { return this.#control.settled(timeoutMs).then(() => this.status); }

  async requestHandoff(reason, summary) {
    const intervention = browserIntervention(reason, summary);
    await this.#control.pause();
    this.#intervention = intervention;
    this.#notify();
    return intervention;
  }

  // Lease-holder video (desktop: RFB). One at a time; every chunk rechecks
  // the exclusive lease before it leaves this runtime.
  async *stream(principal, leaseID, { signal, desktop = false } = {}) {
    const factory = desktop ? this.#desktopFactory : this.#videoFactory;
    if (!factory || signal?.aborted) throw new Error('Browser video is unavailable');
    const controller = new AbortController();
    let video;
    const abort = () => controller.abort();
    try {
      await this.#control.human(principal, leaseID, async () => {
        if (this.#leased) throw new Error();
        this.#leased = controller;
        video = await factory({ signal: controller.signal });
        if (desktop) this.#desktop = video;
      });
      signal?.addEventListener('abort', abort, { once: true });
      if (signal?.aborted) controller.abort();
      for await (const chunk of video) {
        if (controller.signal.aborted) break;
        await this.#control.human(principal, leaseID, () => {});
        if (controller.signal.aborted) break;
        yield chunk;
      }
    } catch { throw new Error('Browser video is unavailable'); }
    finally {
      signal?.removeEventListener('abort', abort);
      controller.abort();
      video?.close();
      if (this.#desktop === video) this.#desktop = undefined;
      if (this.#leased === controller) this.#leased = undefined;
    }
  }

  // View-only video for the authorized chat. It never accepts input and is
  // available whatever the control state, until the browser closes.
  async *watch({ signal } = {}) {
    if (!this.#videoFactory || signal?.aborted || this.status === 'none' ||
      this.#watchers.size >= this.#maxWatchers) throw new Error('Browser video is unavailable');
    const controller = new AbortController();
    this.#watchers.add(controller);
    const abort = () => controller.abort();
    signal?.addEventListener('abort', abort, { once: true });
    let video;
    try {
      video = await this.#videoFactory({ signal: controller.signal });
      for await (const chunk of video) {
        if (controller.signal.aborted || this.status === 'none') break;
        yield chunk;
      }
    } catch { throw new Error('Browser video is unavailable'); }
    finally {
      signal?.removeEventListener('abort', abort);
      controller.abort();
      video?.close();
      this.#watchers.delete(controller);
    }
  }

  async writeDesktop(principal, leaseID, bytes) {
    const desktop = this.#desktop;
    if (!desktop) throw new Error('Desktop unavailable');
    await this.#control.human(principal, leaseID, () => desktop.write(bytes));
  }

  async handle(principal, command) {
    try {
      if (!command || typeof command !== 'object' || Array.isArray(command) || this.status === 'none') throw new Error();
      const allowed = command.type === 'claim' ? ['type'] : ['type', 'leaseID', 'input'];
      if (Object.keys(command).some(key => !allowed.includes(key))) throw new Error();
      if (command.type === 'claim') {
        const lease = await this.#control.takeOver(principal, this.#leaseMs);
        this.#notify();
        return lease;
      }
      if (typeof command.leaseID !== 'string') throw new Error();
      if (command.type === 'input') return await this.#view.dispatch(principal, command.leaseID, command.input);
      if (command.input !== undefined) throw new Error();
      if (command.type === 'resume') {
        await this.#control.human(principal, command.leaseID, async () => {
          // Releasing held mouse buttons is best effort; it must not trap the user.
          await Promise.resolve(this.#view.release?.()).catch(() => {});
          this.#returning = true;
          await this.#desktop?.close();
          this.#leased?.abort();
        });
        try { await this.#control.returnControl(principal, command.leaseID); }
        finally { this.#returning = false; }
        this.#intervention = undefined;
        this.#notify();
        return { type: 'ack' };
      }
      if (command.type === 'cancel') {
        // Stopping the native stream is part of explicit return, not a stop.
        if (this.#returning) throw new Error();
        // Check ownership before closing; another user cannot stop the task.
        await this.#control.human(principal, command.leaseID, () => {});
        await this.#control.close();
        return { type: 'ack' };
      }
      throw new Error();
    } catch {
      throw new Error('Browser control request could not be completed');
    }
  }

  close() { return this.#control.close(); }
}
