// Private worker channel. The parent authenticates every stream and renews its
// authority; this relay rechecks the human lease through BrowserHandoff.stream.
// One acknowledged chunk at a time bounds memory without blocking input IPC.
export class BrowserVideoIPC {
  #handoff;
  #process;
  #active;
  #signal;
  #abort;

  constructor({ handoff, processRef, signal }) {
    this.#handoff = handoff;
    this.#process = processRef;
    this.#signal = signal;
    this.#abort = () => this.#active?.controller.abort();
    signal.addEventListener('abort', this.#abort, { once: true });
    processRef.once('disconnect', this.#abort);
  }

  handle(message) {
    if (message?.type === 'browser-video-ack') {
      if (message.id === this.#active?.id && message.sequence === this.#active?.sequence) this.#active.ack?.();
      return;
    }
    if (message?.type === 'browser-video-stop') {
      if (message.id === this.#active?.id) this.#active.controller.abort();
      return;
    }
    if (message?.type !== 'browser-video-start') return;
    if (this.#active || this.#signal.aborted || typeof message.id !== 'string' ||
      !/^[a-zA-Z0-9_-]{1,128}$/.test(message.id)) return;
    const active = { id: message.id, sequence: 0, controller: new AbortController() };
    this.#active = active;
    void this.#run(active, message);
  }

  async #run(active, message) {
    let failed = false;
    try {
      const video = this.#handoff.stream(message.principal, message.leaseID, { signal: active.controller.signal });
      for await (const chunk of video) {
        for (let offset = 0; offset < chunk.length; offset += 65536) {
          const bytes = chunk.subarray(offset, offset + 65536);
          await this.#send(active, bytes);
        }
      }
    } catch { failed = true; }
    finally {
      active.controller.abort();
      if (this.#active === active) this.#active = undefined;
      if (this.#process.connected) {
        try { this.#process.send({ type: 'browser-video-end', id: active.id, failed }, () => {}); } catch { /* closed */ }
      }
      // Loss of the human's transport cancels, never silently resumes, the
      // browser. A completed explicit return has already cleared the handoff.
      await this.#handoff.handle(message.principal, { type: 'cancel', leaseID: message.leaseID }).catch(() => {});
    }
  }

  #send(active, bytes) {
    return new Promise((resolve, reject) => {
      if (active.controller.signal.aborted || !this.#process.connected) return reject(new Error('Video closed'));
      let settled = false;
      const finish = ok => {
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        active.controller.signal.removeEventListener('abort', abort);
        active.ack = undefined;
        ok ? resolve() : reject(new Error('Video closed'));
      };
      const abort = () => finish(false);
      const timer = setTimeout(abort, 5000);
      active.controller.signal.addEventListener('abort', abort, { once: true });
      active.sequence++;
      active.ack = () => finish(true);
      try {
        this.#process.send({ type: 'browser-video-chunk', id: active.id,
          sequence: active.sequence, bytes: bytes.toString('base64') }, error => { if (error) abort(); });
      } catch { abort(); }
    });
  }

  close() {
    this.#abort();
    this.#signal.removeEventListener('abort', this.#abort);
    this.#process.off('disconnect', this.#abort);
  }
}
