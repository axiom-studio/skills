import { randomUUID } from 'node:crypto';

// Parent-side bounded relay. The host must renew authorization before each
// proof expires; a stream never inherits the browser runtime's longer lifetime.
export class BrowserVideoRelay {
  #child; #id = randomUUID(); #owner; #timer; #pending; #wake;
  #closed = false; #failed = false; #receive; #fail;
  constructor({ child, principal, leaseID }) {
    this.#child = child;
    this.#owner = { userID: principal.userID, tenantID: principal.tenantID, agentID: principal.agentID };
    this.#receive = message => {
      if (message?.id !== this.#id) return;
      if (message.type === 'browser-video-end') return this.close(message.failed);
      if (message.type !== 'browser-video-chunk') return;
      if (this.#pending || !Number.isSafeInteger(message.sequence) || message.sequence < 1 ||
        typeof message.bytes !== 'string' || message.bytes.length > 87384 ||
        !/^[A-Za-z0-9+/]+={0,2}$/.test(message.bytes)) return this.close(true);
      const bytes = Buffer.from(message.bytes, 'base64');
      if (!bytes.length || bytes.length > 65536) return this.close(true);
      this.#pending = { bytes, sequence: message.sequence };
      this.#wake?.();
    };
    this.#fail = () => this.close(true);
    this.renew(principal);
    child.on('message', this.#receive);
    child.once('exit', this.#fail);
    child.once('error', this.#fail);
    child.once('disconnect', this.#fail);
    this.#send({ type: 'browser-video-start', principal: this.#owner, leaseID });
  }
  renew(principal) {
    const remaining = Date.parse(principal.expiresAt) - Date.now();
    if (this.#closed || !Number.isFinite(remaining) || remaining <= 0 || remaining > 16000 ||
      Object.keys(this.#owner).some(key => principal[key] !== this.#owner[key])) throw new Error('Browser video authorization failed');
    clearTimeout(this.#timer);
    this.#timer = setTimeout(() => this.close(true), remaining);
    this.#timer.unref?.();
  }
  #send(message) {
    try { this.#child.send({ ...message, id: this.#id }, error => { if (error) this.close(true); }); }
    catch { this.close(true); }
  }
  close(failed = false) {
    if (this.#closed) return;
    this.#closed = true; this.#failed = !!failed;
    clearTimeout(this.#timer);
    this.#child.off('message', this.#receive);
    this.#child.off('exit', this.#fail);
    this.#child.off('error', this.#fail);
    this.#child.off('disconnect', this.#fail);
    this.#pending = undefined;
    this.#send({ type: 'browser-video-stop' });
    this.#wake?.();
  }
  async *[Symbol.asyncIterator]() {
    try {
      while (!this.#closed) {
        if (!this.#pending) await new Promise(resolve => { this.#wake = resolve; });
        this.#wake = undefined;
        if (this.#closed) break;
        const packet = this.#pending;
        // ACK only after the consumer has accepted these bytes. No second
        // packet or input lock is held while the network applies backpressure.
        yield packet.bytes;
        this.#pending = undefined;
        if (!this.#closed) this.#send({ type: 'browser-video-ack', sequence: packet.sequence });
      }
      if (this.#failed) throw new Error('Browser video is unavailable');
    } finally { this.close(); }
  }
}
