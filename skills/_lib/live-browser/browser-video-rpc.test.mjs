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
  stream.destroy = error => { stream.destroyed = true; stream.emit('close'); };
  // grpc-js turns an emitted error into the call's status.
  stream.on('error', error => { stream.failure = error; });
  return stream;
}
const packet = authorization => ({ value: Buffer.from(JSON.stringify({ agentID: 'agent', sessionID: 'session',
  authorization, commandJSON: JSON.stringify({ type: 'video', leaseID: 'lease' }) })) });

test('desktop RPC separates authority from RFB and acknowledges the ordered input barrier', async () => {
  const rpc = call(), inputs = []; let stop;
  browserVideoRPC({ async videoBrowser(request, desktop) {
    assert.equal(desktop, true);
    return { stream: (async function* () { yield Buffer.from('RFB'); await new Promise(resolve => { stop = resolve; }); })(),
      write: async bytes => inputs.push(bytes.toString()), close: () => stop?.(), renew: async () => {} };
  } }, rpc, true);
  rpc.emit('data', { value: Buffer.concat([Buffer.from([0]), packet('proof').value]) });
  await setImmediate();
  assert.deepEqual([...rpc.output[0].value], [1,82,70,66]);
  rpc.emit('data', { value: Buffer.from([1, 65]) }); await setImmediate();
  rpc.emit('data', { value: Buffer.from([2]) }); await setImmediate();
  assert.deepEqual(inputs, ['A']);
  assert.deepEqual([...rpc.output.at(-1).value], [2]);
  rpc.emit('cancelled');
});
test('desktop barrier survives a full send buffer while frames are in flight', async () => {
  const rpc = call(); let stop;
  browserVideoRPC({ async videoBrowser() {
    return { stream: (async function* () { yield Buffer.from('RFB'); await new Promise(resolve => { stop = resolve; }); })(),
      write: async () => {}, close: () => stop?.(), renew: async () => {} };
  } }, rpc, true);
  rpc.emit('data', { value: Buffer.concat([Buffer.from([0]), packet('proof').value]) });
  await setImmediate();
  rpc.write = packet => { rpc.output.push(packet); return false; };
  rpc.emit('data', { value: Buffer.from([2]) }); await setImmediate();
  assert.equal(rpc.failure, undefined);
  assert.deepEqual([...rpc.output.at(-1).value], [2]);
  assert.equal(rpc.paused, false);
  rpc.emit('cancelled');
});
test('desktop rejects native input before authentication', async () => {
  const rpc = call(); let opened = false;
  browserVideoRPC({ videoBrowser: async () => { opened = true; } }, rpc, true);
  rpc.emit('data', { value: Buffer.from([1, 65]) }); await setImmediate();
  assert.equal(opened, false); assert.equal(rpc.failure.code, 7);
});

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
test('a refused stream reaches the gRPC client as a status at once, not after its deadline', { timeout: 5000 }, async () => {
  const { grpc, addBrowserControlService } = await import('./browser-grpc.mjs');
  const server = new grpc.Server();
  addBrowserControlService(server, { videoBrowser: async () => { throw new Error('no slot'); }, controlBrowser: async () => ({}) });
  const port = await new Promise((resolve, reject) => server.bindAsync('127.0.0.1:0', grpc.ServerCredentials.createInsecure(),
    (error, bound) => error ? reject(error) : resolve(bound)));
  const client = new grpc.Client(`127.0.0.1:${port}`, grpc.credentials.createInsecure());
  try {
    const started = Date.now();
    const status = await new Promise(resolve => {
      const stream = client.makeBidiStreamRequest('/axiom.browser.v1.BrowserControlService/Video', value => value, value => value, {},
        { deadline: Date.now() + 5000 });
      stream.on('data', () => {});
      stream.on('error', error => resolve(error.code));
      stream.write(Buffer.concat([Buffer.from([0x0a, packet('proof').value.length]), packet('proof').value]));
    });
    assert.equal(status, grpc.status.PERMISSION_DENIED);
    assert.ok(Date.now() - started < 1000);
  } finally { client.close(); server.forceShutdown(); }
});
