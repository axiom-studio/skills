import test from 'node:test';
import assert from 'node:assert/strict';
import { TranscriptQueue } from './transcript-queue.mjs';

test('utterances reach chat in order while an agent reply remains pending', async () => {
  const posted = [], replies = [];
  let release;
  const waiting = new Promise(resolve => { release = resolve; });
  const queue = new TranscriptQueue({ signal: new AbortController().signal,
    transcribe: async pcm => pcm.toString(), postUtterance: async text => { posted.push(text); return { id: text }; },
    reply: async message => { replies.push(message.id); await waiting; }, onError: () => assert.fail('unexpected failure') });
  queue.enqueue(Buffer.from('one'));
  await queue.drain();
  queue.enqueue(Buffer.from('two'));
  queue.enqueue(Buffer.from('three'));
  await queue.drain();
  assert.deepEqual(posted, ['one', 'two', 'three']);
  assert.deepEqual(replies, ['one']);
  assert.equal(queue.pendingBytes, 0);
  release();
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(replies, ['one', 'three']);
});

test('bounded backlog and transcription failure stop capture explicitly', async () => {
  for (const overflow of [true, false]) {
    let errors = 0;
    const queue = new TranscriptQueue({ signal: new AbortController().signal, maxPendingBytes: 2,
      transcribe: async () => { throw new Error('private provider diagnostics'); },
      postUtterance: () => assert.fail('must not post'), reply: () => assert.fail('must not reply'),
      onError: () => { errors++; } });
    queue.enqueue(Buffer.from(overflow ? 'large' : 'ok'));
    await queue.drain();
    await Promise.resolve();
    queue.enqueue(Buffer.from('ok'));
    await queue.drain();
    assert.equal(errors, 1);
    assert.equal(queue.pendingBytes, 0);
  }
});

test('aborted sessions do not transcribe queued audio', async () => {
  const controller = new AbortController();
  const queue = new TranscriptQueue({ signal: controller.signal,
    transcribe: () => assert.fail('must not transcribe'), onError: () => assert.fail('must not report cancellation') });
  queue.enqueue(Buffer.from('audio'));
  controller.abort();
  await queue.drain();
  assert.equal(queue.pendingBytes, 0);
});
