// Keep transcription independent of slow agent replies. Audio stays in bounded
// process memory only; a failure stops capture rather than silently losing words.
export class TranscriptQueue {
  constructor({ transcribe, postUtterance, reply, onError, signal, maxPendingBytes = 4 * 1024 * 1024 }) {
    Object.assign(this, { transcribe, postUtterance, reply, onError, signal, maxPendingBytes });
    this.pendingBytes = 0;
    this.tail = Promise.resolve();
    this.replyPending = false;
    this.latestReply = undefined;
    this.failed = false;
  }

  watchReply(utterance) {
    if (this.failed || this.signal.aborted) return;
    if (this.replyPending) {
      this.latestReply = utterance;
      return;
    }
    this.replyPending = true;
    void Promise.resolve().then(() => this.reply(utterance)).catch(() => this.fail('reply_or_playback'))
      .finally(() => {
        this.replyPending = false;
        const next = this.latestReply;
        this.latestReply = undefined;
        if (next) this.watchReply(next);
      });
  }

  fail(stage) {
    if (this.failed || this.signal.aborted) return;
    this.failed = true;
    void Promise.resolve().then(() => this.onError(stage)).catch(() => {});
  }

  enqueue(pcm) {
    this.enqueueItem(pcm, false);
  }

  enqueueText(text) {
    if (typeof text !== 'string' || text.length > 16000) { this.fail('invalid_transcript'); return; }
    if (text.trim()) this.enqueueItem(Buffer.from(text.trim()), true);
  }

  enqueueItem(pcm, committed) {
    if (this.failed || this.signal.aborted) return;
    if (!Buffer.isBuffer(pcm) || this.pendingBytes + pcm.length > this.maxPendingBytes) {
      this.fail('capture_backlog');
      return;
    }
    this.pendingBytes += pcm.length;
    const queuedAt = performance.now();
    this.tail = this.tail.then(async () => {
      let stage = 'transcription';
      try {
        if (this.failed || this.signal.aborted) return;
        console.info(JSON.stringify({ event: 'meeting_voice_stage', stage: committed ? 'transcript_queue' : 'transcription_queue', durationMs: Math.round(performance.now() - queuedAt) }));
        const text = committed ? pcm.toString() : await this.transcribe(pcm);
        if (!text || this.signal.aborted) return;
        stage = 'transcript_post';
        const utterance = await this.postUtterance(text);
        // All utterances reach chat even while a previous reply is pending.
        // Do not build an unbounded queue of stale spoken replies.
        // Retain the latest follow-up instead of losing all pending replies.
        this.watchReply(utterance);
      } catch { this.fail(stage); }
      finally { this.pendingBytes -= pcm.length; }
    });
  }

  async drain() { await this.tail; }
}
