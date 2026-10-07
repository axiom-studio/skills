import test from 'node:test';
import assert from 'node:assert/strict';
import { setImmediate as tick } from 'node:timers/promises';
import { ReplyInbox } from './reply-inbox.mjs';
import { TranscriptQueue } from './transcript-queue.mjs';

const answer = (id, sequence, extra = {}) => ({ sequence, replyToMessageId: id,
  sender: { type: 'agent', id: 'a' }, audience: { kind: 'channel' }, content: id, ...extra });
function setup(options = {}) {
  const controller = new AbortController();
  const delivered = [], errors = [], reports = [];
  const inbox = new ReplyInbox({ agentID: 'a', signal: controller.signal,
    deliver: text => delivered.push(text), onError: error => errors.push(error),
    report: report => reports.push(JSON.parse(report)), sleep: tick, ...options });
  return { inbox, controller, delivered, errors, reports };
}

test('new completed reply bypasses missing old reply; only authorized answers are spoken', async () => {
  let reads = 0;
  const s = setup({ read: async () => ++reads === 1 ? [] : [
    answer('second', 3, { audience: { kind: 'private' } }),
    answer('second', 4, { sender: { type: 'agent', id: 'other' } }),
    answer('unrelated', 5), answer('second', 6), answer('second', 7)] });
  s.inbox.register({ id: 'first', sequence: 1 });
  s.inbox.register({ id: 'second', sequence: 2 });
  for (let n = 0; n < 5; n++) await tick();
  s.controller.abort();
  assert.deepEqual(s.delivered, ['second']);
  assert.deepEqual(s.errors, []);
});

test('registration during fetch rewinds cursor and repeated registration cannot replay audio', async () => {
  let resolveRead;
  const cursors = [];
  const s = setup({ read: async cursor => {
    cursors.push(cursor);
    if (cursors.length === 1) return new Promise(resolve => { resolveRead = resolve; });
    return [answer('late', 15), answer('first', 31)];
  } });
  s.inbox.register({ id: 'first', sequence: 20 });
  s.inbox.register({ id: 'late', sequence: 10 });
  resolveRead([answer('unrelated', 30)]);
  for (let n = 0; n < 5; n++) await tick();
  s.inbox.register({ id: 'late', sequence: 10 });
  await tick();
  assert.deepEqual(cursors, [20, 10]);
  assert.deepEqual(s.delivered, ['late', 'first']);
  s.controller.abort();
});

test('slow playback does not block reads and abort prevents late delivery', async () => {
  let reads = 0, release;
  const delivered = [];
  const s = setup({ read: async () => ++reads === 1 ? [answer('first', 3)] : [answer('second', 4)],
    deliver: text => { delivered.push(text); return new Promise(resolve => { release = resolve; }); } });
  s.inbox.register({ id: 'first', sequence: 1 });
  s.inbox.register({ id: 'second', sequence: 2 });
  for (let n = 0; n < 5; n++) await tick();
  assert.deepEqual(delivered, ['first', 'second']);
  s.controller.abort(); release();
  s.inbox.register({ id: 'third', sequence: 5 });
  assert.equal(reads, 2);
});

test('pending replies expire and capacity failures are explicit', async () => {
  let now = 0;
  const s = setup({ now: () => now, timeoutMs: 10, read: async () => [], sleep: async () => { now = 11; } });
  s.inbox.register({ id: 'first', sequence: 1 });
  await tick();
  assert.equal(s.inbox.pending.size, 0);
  assert.equal(s.reports[0].outcome, 'expired');
  const bounded = setup({ maximum: 1, read: () => new Promise(() => {}) });
  bounded.inbox.register({ id: 'first', sequence: 1 });
  bounded.inbox.register({ id: 'second', sequence: 2 });
  await tick();
  assert.deepEqual(bounded.errors, ['reply_backlog']);
  bounded.controller.abort(); s.controller.abort();
});

test('rapid committed transcripts all register without waiting for replies', async () => {
  const s = setup({ read: async () => [] });
  let sequence = 0;
  const queue = new TranscriptQueue({ signal: s.controller.signal,
    postUtterance: async text => ({ id: text, sequence: ++sequence }),
    reply: message => s.inbox.register(message), onError: error => s.errors.push(error) });
  for (let n = 0; n < 32; n++) queue.enqueueText(`utterance-${n}`);
  await queue.drain(); await tick();
  assert.equal(s.inbox.pending.size, 32);
  assert.deepEqual(s.errors, []);
  s.controller.abort();
});

test('abort during a read suppresses returned audio and is not an error', async () => {
  let finish;
  const s = setup({ read: () => new Promise(resolve => { finish = resolve; }) });
  s.inbox.register({ id: 'first', sequence: 1 });
  s.controller.abort(); finish([answer('first', 2)]);
  await tick();
  assert.deepEqual(s.delivered, []);
  assert.deepEqual(s.errors, []);
  assert.equal(s.inbox.pending.size, 0);
});
