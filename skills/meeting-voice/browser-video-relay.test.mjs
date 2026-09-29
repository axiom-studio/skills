import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { setImmediate, setTimeout as delay } from 'node:timers/promises';
import { BrowserVideoRelay } from './browser-video-relay.mjs';

const principal = () => ({ userID: 'human', tenantID: 'tenant', agentID: 'agent',
  expiresAt: new Date(Date.now() + 15000).toISOString() });
function setup(owner = principal()) {
  const child = new EventEmitter(), sent = [];
  child.send = (message, callback) => { sent.push(message); callback?.(); };
  const relay = new BrowserVideoRelay({ child, principal: owner, leaseID: 'lease' });
  return { child, sent, relay, id: sent[0].id };
}
test('desktop input is bounded, acknowledged, and rejected after proof expiry', async () => {
  const child = new EventEmitter(), sent = [];
  child.send = (message, callback) => { sent.push(message); callback?.(); };
  const relay = new BrowserVideoRelay({ child, principal: principal(), leaseID: 'lease', desktop: true });
  const first = relay.write(Buffer.from('key'));
  await assert.rejects(relay.write(Buffer.from('second')), /unavailable/);
  const message = sent.at(-1);
  child.emit('message', { type: 'browser-desktop-input-ack', id: message.id, sequence: message.sequence });
  await first;
  relay.close();
  await assert.rejects(relay.write(Buffer.from('late')), /unavailable/);
});
test('parent stream has one bounded packet and acknowledges only after consumption', async () => {
  const { child, sent, relay, id } = setup();
  const iterator = relay[Symbol.asyncIterator]();
  const first = iterator.next();
  child.emit('message', { type: 'browser-video-chunk', id, sequence: 1, bytes: Buffer.from('video').toString('base64') });
  assert.equal((await first).value.toString(), 'video');
  assert.equal(sent.length, 1);
  const next = iterator.next();
  await setImmediate();
  assert.equal(sent[1].type, 'browser-video-ack');
  relay.close();
  assert.equal((await next).done, true);
  assert.equal(child.listenerCount('message'), 0);
});
test('proof expiry closes a quiet stream instead of leaving human control active', async () => {
  const { relay, sent } = setup({ ...principal(), expiresAt: new Date(Date.now() + 30).toISOString() });
  const next = relay[Symbol.asyncIterator]().next();
  const rejected = assert.rejects(next, /unavailable/);
  await delay(60);
  await rejected;
  assert.equal(sent.at(-1).type, 'browser-video-stop');
});
test('renewal cannot change the controlling principal or extend authority beyond host proof lifetime', () => {
  const { relay } = setup();
  try {
    for (const owner of [{ ...principal(), userID: 'other' }, { ...principal(), tenantID: 'other' },
      { ...principal(), expiresAt: new Date(Date.now() + 60000).toISOString() }]) assert.throws(() => relay.renew(owner));
    relay.renew(principal());
  } finally { relay.close(); }
});
test('unsolicited second packets fail closed rather than accumulating video in memory', async () => {
  const { child, relay, id } = setup();
  const packet = { type: 'browser-video-chunk', id, sequence: 1, bytes: 'eA==' };
  child.emit('message', packet);
  child.emit('message', { ...packet, sequence: 2 });
  await assert.rejects(relay[Symbol.asyncIterator]().next(), /unavailable/);
});
