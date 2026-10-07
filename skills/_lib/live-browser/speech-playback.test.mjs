import assert from 'node:assert/strict';
import { test } from 'node:test';
import { SpeechPlayback, handleSpeak } from './speech-playback.mjs';

test('chat speech and meeting replies share one serialized playback lane', async () => {
  const controller = new AbortController();
  const events = [];
  let release;
  const blocked = new Promise(resolve => { release = resolve; });
  const playback = new SpeechPlayback(async text => {
    events.push(text);
    if (text === 'meeting reply') await blocked;
  }, controller.signal);
  const first = playback.enqueue('meeting reply');
  const receipts = [];
  const second = handleSpeak({ type: 'speak', id: 'request-1', text: 'Hi Vishnu', deadline: Date.now() + 1000 },
    playback, () => true, receipt => receipts.push(receipt));
  await new Promise(resolve => setImmediate(resolve));
  assert.deepEqual(events, ['meeting reply']);
  assert.equal(receipts.length, 0);
  release();
  await Promise.all([first, second]);
  assert.deepEqual(events, ['meeting reply', 'Hi Vishnu']);
  assert.equal(receipts[0].delivery, 'played');
});

test('failed, expired, canceled and inactive playback never reports played', async () => {
  for (const mode of ['failed', 'expired', 'canceled', 'inactive']) {
    const controller = new AbortController();
    if (mode === 'canceled') controller.abort();
    let called = false;
    const playback = new SpeechPlayback(async () => { called = true; throw new Error('private upstream content'); }, controller.signal);
    let receipt;
    await handleSpeak({ type: 'speak', id: 'request', text: 'Hi', deadline: Date.now() + (mode === 'expired' ? -1 : 1000) },
      playback, () => mode !== 'inactive', result => { receipt = result; });
    assert.equal(receipt.delivery, 'unconfirmed');
    assert.equal(called, mode === 'failed');
    assert.doesNotMatch(JSON.stringify(receipt), /private/);
  }
});
