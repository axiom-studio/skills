import WebSocket from 'ws';

// Parent-only provider connection. Never forward raw provider errors or keys.
export class RealtimeTranscription {
  constructor({ apiKey, onTranscript, onError, socketFactory = (url, options) => new WebSocket(url, options), startupMs = 15000 }) {
    if (typeof apiKey !== 'string' || !apiKey.trim()) throw new Error('Transcription credential is required');
    this.onTranscript = onTranscript;
    this.onError = onError;
    this.closed = false;
    this.started = false;
    this.ready = new Promise((resolve, reject) => { this.resolve = resolve; this.reject = reject; });
    // Failure can arrive before the caller awaits ready.
    this.ready.catch(() => {});
    const url = new URL('wss://api.elevenlabs.io/v1/speech-to-text/realtime');
    url.search = new URLSearchParams({ model_id: 'scribe_v2_realtime', audio_format: 'pcm_16000',
      commit_strategy: 'vad', vad_silence_threshold_secs: '0.5',
      min_speech_duration_ms: '100', min_silence_duration_ms: '100' }).toString();
    this.socket = socketFactory(url, { headers: { 'xi-api-key': apiKey },
      maxPayload: 65536, handshakeTimeout: startupMs, perMessageDeflate: false, followRedirects: false });
    this.timer = setTimeout(() => this.fail('startup_timeout'), startupMs);
    this.timer.unref?.();
    this.socket.on('message', data => this.receive(data));
    this.socket.on('error', () => this.fail('connection'));
    this.socket.on('close', () => { if (!this.closed) this.fail('connection_closed'); });
  }

  receive(data) {
    if (this.closed) return;
    try {
      if (Buffer.byteLength(data) > 65536) throw new Error();
      const message = JSON.parse(data.toString());
      if (message.message_type === 'session_started') {
        this.started = true; clearTimeout(this.timer); this.resolve();
      } else if (message.message_type === 'committed_transcript') {
        if (!this.started || typeof message.text !== 'string' || message.text.length > 16000) throw new Error();
        const text = message.text.trim();
        if (text) this.onTranscript(text);
      } else if (!['partial_transcript', 'committed_transcript_with_timestamps', 'warning'].includes(message.message_type)) {
        this.fail('provider');
      }
    } catch { this.fail('invalid_event'); }
  }

  fail(stage) {
    if (this.closed) return;
    this.close();
    this.onError(stage);
  }

  async send(pcm) {
    await this.ready;
    if (this.closed || !Buffer.isBuffer(pcm) || pcm.length < 2 || pcm.length > 6400 || pcm.length % 2) {
      throw new Error('Realtime transcription audio is invalid');
    }
    if (this.socket.bufferedAmount > 64000) {
      this.fail('backlog'); throw new Error('Realtime transcription is behind');
    }
    await new Promise((resolve, reject) => {
      this.socket.send(JSON.stringify({ message_type: 'input_audio_chunk', audio_base_64: pcm.toString('base64'), sample_rate: 16000 }), error => {
        if (error) { this.fail('delivery'); reject(new Error('Realtime transcription delivery failed')); }
        else resolve();
      });
    });
  }

  close() {
    if (this.closed) return;
    this.closed = true; clearTimeout(this.timer);
    this.reject(new Error('Realtime transcription closed'));
    this.socket.terminate();
  }
}

// Child-side bounded capture pump: at most 2 seconds of unsent audio. This
// streams 100ms frames, not silence-delimited multi-second uploads.
export class RealtimeCapture {
  constructor({ send, onError, signal, maxBytes = 64000 }) {
    Object.assign(this, { send, onError, signal, maxBytes });
    this.pending = Buffer.alloc(0);
    this.busy = false;
    this.failed = false;
  }
  feed(bytes) {
    if (this.failed || this.signal.aborted) return;
    if (!Buffer.isBuffer(bytes) || this.pending.length + bytes.length > this.maxBytes) {
      this.failed = true; this.pending = Buffer.alloc(0); this.onError('realtime_capture_backlog'); return;
    }
    this.pending = Buffer.concat([this.pending, bytes]);
    if (!this.busy) void this.flush();
  }
  async flush() {
    this.busy = true;
    try {
      while (this.pending.length >= 3200 && !this.signal.aborted && !this.failed) {
        const frame = this.pending.subarray(0, 3200);
        this.pending = this.pending.subarray(3200);
        await this.send(frame);
      }
    } catch {
      if (!this.failed && !this.signal.aborted) { this.failed = true; this.onError('realtime_audio_delivery'); }
    } finally {
      this.busy = false;
      if (this.failed || this.signal.aborted) this.pending = Buffer.alloc(0);
    }
  }
}
