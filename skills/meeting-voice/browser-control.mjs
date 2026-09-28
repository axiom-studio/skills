import { randomUUID } from 'node:crypto';

// Runtime concurrency boundary, not authentication middleware. The transport
// must resolve these identities from authenticated, authorized host context.
// No browser content, input events, cookies or credentials are retained here.
export class BrowserControl {
  #scope;
  #state = 'automation';
  #lease;
  #tail = Promise.resolve();
  #timer;
  #close;
  #now;

  constructor({ tenantID, agentID, close, now = Date.now }) {
    if (!tenantID || !agentID || typeof close !== 'function') throw new Error('Browser owner and cleanup are required');
    this.#scope = { tenantID, agentID };
    this.#close = close;
    this.#now = now;
  }

  get state() { return this.#state; }

  #authorized(principal) {
    if (typeof principal?.userID !== 'string' || !principal.userID.trim() ||
      principal.tenantID !== this.#scope.tenantID || principal.agentID !== this.#scope.agentID) {
      throw new Error('Browser control denied');
    }
  }

  #queue(operation) {
    const result = this.#tail.then(operation);
    this.#tail = result.catch(() => {});
    return result;
  }

  automate(operation) {
    return this.#queue(() => {
      if (this.#state !== 'automation') throw new Error('Browser automation is paused');
      return operation();
    });
  }

  async takeOver(principal, ttlMs = 300000) {
    this.#authorized(principal);
    if (!Number.isInteger(ttlMs) || ttlMs < 1000 || ttlMs > 600000) throw new Error('Invalid browser control lifetime');
    if (this.#state !== 'automation') throw new Error('Browser control is already reserved');
    // Reserve synchronously so no further automation can race the handoff.
    this.#state = 'transferring';
    await this.#tail;
    if (this.#state !== 'transferring') throw new Error('Browser control is closed');
    this.#lease = { id: randomUUID(), userID: principal.userID, expiresAt: this.#now() + ttlMs };
    this.#state = 'human';
    this.#timer = setTimeout(() => { void this.close().catch(() => {}); }, ttlMs);
    this.#timer.unref?.();
    return { id: this.#lease.id, expiresAt: this.#lease.expiresAt };
  }

  #checkLease(principal, leaseID) {
    this.#authorized(principal);
    if (this.#state !== 'human' || this.#lease?.id !== leaseID || this.#lease.userID !== principal.userID) {
      throw new Error('Browser control denied');
    }
    if (this.#now() >= this.#lease.expiresAt) {
      void this.close().catch(() => {});
      throw new Error('Browser control expired');
    }
  }

  human(principal, leaseID, operation) {
    return this.#queue(() => {
      this.#checkLease(principal, leaseID);
      return operation();
    });
  }

  async returnControl(principal, leaseID) {
    this.#checkLease(principal, leaseID);
    this.#state = 'transferring';
    await this.#tail;
    if (this.#state !== 'transferring' || this.#now() >= this.#lease.expiresAt) {
      await this.close();
      throw new Error('Browser control expired');
    }
    clearTimeout(this.#timer);
    this.#lease = undefined;
    this.#state = 'automation';
  }

  async close() {
    if (this.#state === 'closed') return;
    this.#state = 'closed';
    clearTimeout(this.#timer);
    this.#lease = undefined;
    // Close immediately, rather than waiting indefinitely for browser I/O.
    await this.#close();
  }
}
