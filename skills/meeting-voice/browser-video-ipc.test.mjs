import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { setImmediate } from 'node:timers/promises';
import { BrowserVideoIPC } from './browser-video-ipc.mjs';

test('stream chunks wait for matching acknowledgements and disconnect cancels control', async () => {
  const processRef = new EventEmitter(), sent = [], canceled = [];
  processRef.connected = true;
  processRef.send = (message, callback) => { sent.push(message); callback?.(); };
  const controller = new AbortController();
  const relay = new BrowserVideoIPC({ processRef, signal: controller.signal, handoff: {
    async *stream(principal, leaseID, { signal }) {
      assert.equal(principal.userID, 'human');
      assert.equal(leaseID, 'lease');
      yield Buffer.alloc(100000, 42);
      await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
    },
    async handle(principal, command) { canceled.push([principal, command]); },
  } });
  relay.handle({ type: 'browser-video-start', id: 'stream1', principal: { userID: 'human' }, leaseID: 'lease' });
  await setImmediate();
  assert.equal(sent.length, 1);
  assert.equal(Buffer.from(sent[0].bytes, 'base64').length, 65536);
  relay.handle({ type: 'browser-video-ack', id: 'other', sequence: 1 });
  await setImmediate();
  assert.equal(sent.length, 1);
  relay.handle({ type: 'browser-video-ack', id: 'stream1', sequence: 1 });
  await setImmediate();
  assert.equal(Buffer.from(sent[1].bytes, 'base64').length, 34464);
  processRef.emit('disconnect');
  await setImmediate();
  assert.equal(canceled[0][1].type, 'cancel');
  relay.close();
  assert.equal(processRef.listenerCount('disconnect'), 0);
});

test('authorization failure emits only a sanitized terminal packet', async () => {
  const processRef = new EventEmitter(), sent = [];
  processRef.connected = true;
  processRef.send = message => sent.push(message);
  const relay = new BrowserVideoIPC({ processRef, signal: new AbortController().signal, handoff: {
    async *stream() { throw new Error('private browser detail'); }, async handle() {},
  } });
  relay.handle({ type: 'browser-video-start', id: 'stream2', principal: {}, leaseID: 'bad' });
  await setImmediate();
  assert.deepEqual(sent, [{ type: 'browser-video-end', id: 'stream2', failed: true }]);
  relay.close();
});

test('late send callbacks cannot clear the next chunk acknowledgement', async () => {
  const processRef = new EventEmitter(), sent = [], callbacks = [];
  processRef.connected = true;
  processRef.send = (message, callback) => { sent.push(message); callbacks.push(callback); };
  const relay = new BrowserVideoIPC({ processRef, signal: new AbortController().signal, handoff: {
    async *stream() { yield Buffer.alloc(100000); }, async handle() {},
  } });
  relay.handle({ type: 'browser-video-start', id: 'stream3', principal: {}, leaseID: 'lease' });
  await setImmediate();
  relay.handle({ type: 'browser-video-ack', id: 'stream3', sequence: 1 });
  await setImmediate();
  assert.equal(sent[1].sequence, 2);
  callbacks[0](new Error('late transport callback'));
  relay.handle({ type: 'browser-video-ack', id: 'stream3', sequence: 2 });
  await setImmediate();
  assert.deepEqual(sent[2], { type: 'browser-video-end', id: 'stream3', failed: false });
  relay.close();
});
