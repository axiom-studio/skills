// Pull-based private IPC: one active synthesis and one <=200ms PCM packet per
// request. Provider credentials and provider error bodies stay in the parent.
export class SpeechStreamHost {
  constructor(synthesize, { idleMs = 45000 } = {}) {
    this.synthesize = synthesize;
    this.idleMs = idleMs;
  }

  close() {
    const active = this.active;
    this.active = undefined;
    if (!active) return;
    clearTimeout(active.timer);
    active.controller.abort();
    void active.iterator.return?.().catch(() => {});
  }

  async handle(message) {
    if (message.operation === 'stream-start') {
      if (this.active) throw new Error('Speech stream is occupied');
      const controller = new AbortController();
      this.active = { id: message.id, controller,
        iterator: this.synthesize(message.text, controller.signal)[Symbol.asyncIterator](), busy: false };
    }
    const active = this.active;
    if (!active || (message.operation !== 'stream-start' && message.streamID !== active.id)) {
      throw new Error('Speech stream is unavailable');
    }
    if (message.operation === 'stream-cancel') { this.close(); return { done: true }; }
    if (!['stream-start', 'stream-next'].includes(message.operation) || active.busy) {
      throw new Error('Speech stream request is invalid');
    }
    active.busy = true;
    clearTimeout(active.timer);
    active.timer = setTimeout(() => this.close(), this.idleMs);
    active.timer.unref?.();
    try {
      const { value, done } = await active.iterator.next();
      if (this.active !== active) throw new Error('Speech stream canceled');
      if (done) { this.close(); return { streamID: active.id, done: true }; }
      if (!Buffer.isBuffer(value) || !value.length || value.length > 6400 || value.length % 2) {
        throw new Error('Speech PCM packet is invalid');
      }
      return { streamID: active.id, audio: value.toString('base64'), done: false };
    } catch (error) {
      if (this.active === active) this.close();
      throw error;
    } finally { active.busy = false; }
  }
}
