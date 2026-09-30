// Keep transcription independent of slow agent replies. Audio stays in bounded
// process memory only; a failure stops capture rather than silently losing words.
export class TranscriptQueue {
  constructor({ transcribe, postUtterance, reply, onError, signal, maxPendingBytes = 4 * 1024 * 1024 }) {
    Object.assign(this, { transcribe, postUtterance, reply, onError, signal, maxPendingBytes });
    this.pendingBytes = 0;
    this.tail = Promise.resolve();
    this.replyTail = Promise.resolve();
    this.pendingReplies = 0;
    this.failed = false;
  }

  fail() {
    if (this.failed || this.signal.aborted) return;
    this.failed = true;
    void Promise.resolve().then(() => this.onError()).catch(() => {});
  }

  enqueue(pcm) {
    if (this.failed || this.signal.aborted) return;
    if (!Buffer.isBuffer(pcm) || this.pendingBytes + pcm.length > this.maxPendingBytes) {
      this.fail();
      return;
    }
    this.pendingBytes += pcm.length;
    this.tail = this.tail.then(async () => {
      try {
        if (this.failed || this.signal.aborted) return;
        const text = await this.transcribe(pcm);
        if (!text || this.signal.aborted) return;
        const utterance = await this.postUtterance(text);
        // Passive events return null. Addressed turns are delivered in order;
        // never create a Run whose response will then be silently skipped.
        if (utterance && !this.signal.aborted) {
          if (this.pendingReplies >= 8) { this.fail(); return; }
          this.pendingReplies++;
          this.replyTail = this.replyTail.then(async () => {
            if (!this.failed && !this.signal.aborted) await this.reply(utterance);
          }).catch(() => this.fail()).finally(() => { this.pendingReplies--; });
        }
      } catch { this.fail(); }
      finally { this.pendingBytes -= pcm.length; }
    });
  }

  async drain() { await this.tail; }
}
