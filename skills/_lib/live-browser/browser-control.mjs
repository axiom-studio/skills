import { randomUUID } from 'node:crypto';

// Runtime concurrency boundary, not authentication middleware. The transport
// must resolve these identities from authenticated, authorized host context.
// No browser content, input events, cookies or credentials are retained here.
//
// States: automation -> transferring -> human -> transferring -> automation.
// A pausable control (onExpire: 'pause') also has `paused`: automation is
// blocked, no lease is held, and a human may claim. Lease expiry then pauses
// rather than closing the browser. Neither mode ever resumes automation
// without an explicit return by the lease owner.
export class BrowserControl {
  #scope;
  #state = 'automation';
  #lease;
  #tail = Promise.resolve();
  #timer;
  #close;
  #now;
  #onExpire;
  #waiters = new Set();

  constructor({ tenantID, agentID, close, now = Date.now, onExpire = 'close' }) {
    if (!tenantID || !agentID || typeof close !== 'function' || !['close', 'pause'].includes(onExpire)) {
      throw new Error('Browser owner and cleanup are required');
    }
    this.#scope = { tenantID, agentID };
    this.#close = close;
    this.#now = now;
    this.#onExpire = onExpire;
  }

  get state() { return this.#state; }

  // Public lease metadata only; never the lease ID or its owner.
  get lease() { return this.#state === 'human' && this.#lease ? { expiresAt: this.#lease.expiresAt } : undefined; }

  #set(state) {
    this.#state = state;
    for (const wake of this.#waiters) wake();
    this.#waiters.clear();
  }

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

  // Resolves once no human holds or is acquiring control, or after timeoutMs.
  async settled(timeoutMs) {
    const deadline = this.#now() + timeoutMs;
    while (['human', 'transferring'].includes(this.#state) && this.#now() < deadline) {
      const remaining = Math.min(deadline, this.#lease?.expiresAt ?? deadline) - this.#now();
      await new Promise(resolve => {
        const timer = setTimeout(done, Math.max(1, remaining));
        timer.unref?.();
        function done() { clearTimeout(timer); resolve(); }
        this.#waiters.add(done);
      });
      if (this.#lease && this.#now() >= this.#lease.expiresAt) this.#expire();
    }
    return this.#state;
  }

  // Pause automation after in-flight work, for an agent-requested handoff.
  pause() {
    if (this.#onExpire !== 'pause') return Promise.reject(new Error('Browser control cannot pause'));
    return this.#queue(() => {
      if (this.#state !== 'automation' && this.#state !== 'paused') throw new Error('Browser automation is paused');
      this.#set('paused');
    });
  }

  async takeOver(principal, ttlMs = 300000) {
    this.#authorized(principal);
    if (!Number.isInteger(ttlMs) || ttlMs < 1000 || ttlMs > 600000) throw new Error('Invalid browser control lifetime');
    if (this.#state !== 'automation' && this.#state !== 'paused') throw new Error('Browser control is already reserved');
    // Reserve synchronously so no further automation can race the handoff.
    this.#set('transferring');
    await this.#tail;
    if (this.#state !== 'transferring') throw new Error('Browser control is closed');
    this.#lease = { id: randomUUID(), userID: principal.userID, expiresAt: this.#now() + ttlMs };
    this.#set('human');
    this.#timer = setTimeout(() => this.#expire(), ttlMs);
    this.#timer.unref?.();
    return { id: this.#lease.id, expiresAt: this.#lease.expiresAt };
  }

  #expire() {
    if (this.#onExpire === 'pause') {
      if (this.#state !== 'human' && this.#state !== 'transferring') return;
      clearTimeout(this.#timer);
      this.#lease = undefined;
      this.#set('paused');
      return;
    }
    void this.close().catch(() => {});
  }

  #checkLease(principal, leaseID) {
    this.#authorized(principal);
    if (this.#state !== 'human' || this.#lease?.id !== leaseID || this.#lease.userID !== principal.userID) {
      throw new Error('Browser control denied');
    }
    if (this.#now() >= this.#lease.expiresAt) {
      this.#expire();
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
    this.#set('transferring');
    await this.#tail;
    if (this.#state !== 'transferring' || this.#now() >= this.#lease.expiresAt) {
      if (this.#onExpire === 'pause' && this.#state === 'transferring') this.#expire();
      else await this.close();
      throw new Error('Browser control expired');
    }
    clearTimeout(this.#timer);
    this.#lease = undefined;
    this.#set('automation');
  }

  async close() {
    if (this.#state === 'closed') return;
    this.#set('closed');
    clearTimeout(this.#timer);
    this.#lease = undefined;
    // Close immediately, rather than waiting indefinitely for browser I/O.
    await this.#close();
  }
}
