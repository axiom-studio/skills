import { setTimeout as delay } from 'node:timers/promises';

// One canonical-message reader for all pending utterances. A missing/canceled
// older agent turn must not block delivery of a newer completed answer.
export class ReplyInbox {
  constructor({ read, agentID, deliver, onError, signal, timeoutMs = 90000, maximum = 64,
    now = Date.now, sleep = () => delay(500, undefined, { signal }), report = console.info }) {
    Object.assign(this, { read, agentID: String(agentID), deliver, onError, signal, timeoutMs, maximum, now, sleep, report });
    this.pending = new Map();
    this.seen = new Set();
    this.rewind = undefined;
    this.cursor = undefined;
    this.running = false;
    this.failed = false;
  }

  register(message) {
    if (this.failed || this.signal.aborted) return;
    if (typeof message?.id !== 'string' || !message.id || !Number.isSafeInteger(message.sequence) || message.sequence < 0) {
      this.fail('invalid_reply_trigger'); return;
    }
    if (this.pending.has(message.id) || this.seen.has(message.id)) return;
    if (this.pending.size >= this.maximum) { this.fail('reply_backlog'); return; }
    this.pending.set(message.id, { sequence: message.sequence, started: this.now() });
    this.seen.add(message.id);
    if (this.seen.size > this.maximum * 2) this.seen.delete(this.seen.values().next().value);
    this.rewind = this.rewind === undefined ? message.sequence : Math.min(this.rewind, message.sequence);
    // Posting and polling can race; rewind for late registration. Delivered
    // triggers are removed, so replayed message pages cannot replay speech.
    this.cursor = this.cursor === undefined ? message.sequence : Math.min(this.cursor, message.sequence);
    if (!this.running) void this.poll();
  }

  fail(stage) {
    if (this.failed || this.signal.aborted) return;
    this.failed = true;
    this.pending.clear();
    void Promise.resolve().then(() => this.onError(stage)).catch(() => {});
  }

  async poll() {
    this.running = true;
    try {
      while (this.pending.size && !this.signal.aborted && !this.failed) {
        for (const [id, pending] of this.pending) {
          if (this.now() - pending.started >= this.timeoutMs) {
            this.pending.delete(id);
            this.report(JSON.stringify({ event: 'meeting_voice_stage', stage: 'agent_reply', outcome: 'expired', durationMs: this.now() - pending.started }));
          }
        }
        if (!this.pending.size) break;
        this.rewind = undefined;
        const messages = await this.read(this.cursor, AbortSignal.any([this.signal, AbortSignal.timeout(15000)]));
        if (this.signal.aborted || this.failed) break;
        for (const message of messages) {
          if (!Number.isSafeInteger(message.sequence)) throw new Error('Invalid message sequence');
          this.cursor = Math.max(this.cursor, message.sequence);
          const pending = this.pending.get(message.replyToMessageId);
          if (!pending || message.sequence <= pending.sequence || message.sender?.type !== 'agent' ||
              String(message.sender.id) !== this.agentID || message.audience?.kind !== 'channel' ||
              typeof message.content !== 'string' || !message.content.trim()) continue;
          this.pending.delete(message.replyToMessageId);
          this.report(JSON.stringify({ event: 'meeting_voice_stage', stage: 'agent_reply', outcome: 'ok', durationMs: this.now() - pending.started }));
          // Playback has its own bounded serial lane. Never block reads on it.
          void Promise.resolve().then(() => {
            if (!this.signal.aborted && !this.failed) return this.deliver(message.content.trim());
          }).catch(() => this.fail('reply_playback'));
        }
        // Registrations during the fetch may precede its cursor. Re-read that
        // range next time rather than skipping replies created before registration.
        if (this.rewind !== undefined) this.cursor = Math.min(this.cursor, this.rewind);
        if (this.pending.size) await this.sleep();
      }
    } catch { this.fail('reply_read'); }
    finally { this.running = false; if (this.signal.aborted || this.failed) this.pending.clear(); }
  }
}
