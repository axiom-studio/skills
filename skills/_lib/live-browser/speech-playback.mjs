// One output lane for both spoken replies and explicit chat speech.
export class SpeechPlayback {
  constructor(play, signal) { this.play = play; this.signal = signal; this.tail = Promise.resolve(); this.pending = 0; }
  enqueue(text, deadline = Infinity) {
    if (this.pending >= 4) return Promise.reject(new Error('Speech queue is full'));
    this.pending++;
    const task = this.tail.then(async () => {
      if (this.signal.aborted || Date.now() >= deadline) throw new Error('Speech request expired or canceled');
      await this.play(text);
    });
    this.tail = task.catch(() => {}).finally(() => { this.pending--; });
    return task;
  }
}

export function handleSpeak(message, playback, available, send) {
  if (message?.type !== 'speak') return;
  if (typeof message.id !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$/.test(message.id)) return;
  const reply = delivery => send({ type: 'speak-result', id: message.id, delivery });
  if (!available() || typeof message.text !== 'string' || !message.text.trim() || message.text.length > 500 ||
      !Number.isFinite(message.deadline) || message.deadline <= Date.now()) { reply('unconfirmed'); return; }
  return playback.enqueue(message.text, message.deadline).then(() => reply('played'), () => reply('unconfirmed'));
}
