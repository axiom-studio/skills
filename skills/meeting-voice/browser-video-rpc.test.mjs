import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { setImmediate } from 'node:timers/promises';
import { browserVideoRPC } from './browser-video-rpc.mjs';

function call() {
  const stream = new EventEmitter();
  stream.output = []; stream.paused = false;
  stream.pause = () => { stream.paused = true; };
  stream.resume = () => { stream.paused = false; };
  stream.write = packet => { stream.output.push(packet); return true; };
  stream.end = () => { stream.ended = true; };
  stream.destroy = error => { stream.failure = error; stream.emit('close'); };
  return stream;
}
const packet = authorization => ({ value: Buffer.from(JSON.stringify({ agentID: 'agent', sessionID: 'session',
  authorization, commandJSON: JSON.stringify({ type: 'video', leaseID: 'lease' }) })) });

test('video RPC accepts fresh proofs independently while streaming encoded bytes', async () => {
  const rpc = call(), renewals = [];
  let stop, closed = 0;
  browserVideoRPC({ async videoBrowser(request) {
    assert.equal(request.command.type, 'video');
    return { stream: (async function* () { yield Buffer.from('encoded video'); await new Promise(resolve => { stop = resolve; }); })(),
      renew: async request => renewals.push(request.authorization.token), close: () => { closed++; stop?.(); } };
  } }, rpc);
  rpc.emit('data', packet('first-proof'));
  await setImmediate();
  assert.equal(rpc.output[0].value.toString(), 'encoded video');
  rpc.emit('data', packet('renewed-proof'));
  await setImmediate();
  assert.deepEqual(renewals, ['renewed-proof']);
  rpc.emit('cancelled');
  await setImmediate();
  assert.equal(closed, 1);
});
test('revoked video authorization closes the session and returns no private error details', async () => {
  const rpc = call(); let stop, closed = false;
  browserVideoRPC({ async videoBrowser() { return {
    stream: (async function* () { await new Promise(resolve => { stop = resolve; }); })(),
    renew: async () => { throw new Error('private detail'); }, close: () => { closed = true; stop(); },
  }; } }, rpc);
  rpc.emit('data', packet('first-proof'));
  await setImmediate();
  rpc.emit('data', packet('revoked-proof'));
  await setImmediate();
  assert.ok(closed);
  assert.equal(rpc.failure.message, 'Browser video is unavailable');
  assert.equal(rpc.failure.code, 7);
});
test('disconnect during initial authorization closes a late-opened stream', async () => {
  const rpc = call(); let open, closed = false;
  browserVideoRPC({ videoBrowser: () => new Promise(resolve => { open = resolve; }) }, rpc);
  rpc.emit('data', packet('first-proof'));
  rpc.emit('cancelled');
  open({ close: () => { closed = true; } });
  await setImmediate();
  assert.ok(closed);
  assert.deepEqual(rpc.output, []);
});
