import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { test } from 'node:test';
import { BrowserHandoff } from './browser-handoff.mjs';

const principal = { tenantID: 'tenant-1', agentID: 'agent-1', userID: 'human-1' };

function fixture(options = {}) {
  const states = [];
  let closed = 0;
  const page = new EventEmitter();
  page.viewportSize = () => ({ width: 1280, height: 800 });
  page.screenshot = async () => Buffer.from('private screen');
  page.keyboard = { insertText: async () => {} };
  const context = { close: async () => { closed++; page.emit('close'); } };
  const handoff = new BrowserHandoff({ ...principal, onState: state => states.push(state), ...options });
  return { handoff, page, context, states, closed: () => closed };
}

test('retains the page privately until the claiming human explicitly resumes', async () => {
  const f = fixture();
  let resumed = false;
  const pending = f.handoff.request(f).then(() => { resumed = true; });
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  assert.deepEqual(f.states, ['awaiting_user']);
  assert.equal(resumed, false);
  const screen = await f.handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'frame' } });
  assert.equal(screen.bytes.toString(), 'private screen');
  assert.equal(resumed, false);
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  await pending;
  assert.deepEqual(f.states, ['awaiting_user', 'joining']);
  assert.equal(f.closed(), 0);
  await assert.rejects(f.handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'frame' } }));
});

test('another user cannot claim, read, cancel, or resume the reserved browser', async () => {
  const f = fixture();
  const pending = f.handoff.request(f);
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  for (const command of [
    { type: 'claim' }, { type: 'input', leaseID: lease.id, input: { type: 'frame' } },
    { type: 'cancel', leaseID: lease.id }, { type: 'resume', leaseID: lease.id },
  ]) await assert.rejects(f.handoff.handle({ ...principal, userID: 'other-user' }, command));
  assert.equal(f.closed(), 0);
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  await pending;
});

test('cancellation closes the browser and never resumes automation', async () => {
  const f = fixture();
  const pending = assert.rejects(f.handoff.request(f), /did not complete/);
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  await f.handoff.handle(principal, { type: 'cancel', leaseID: lease.id });
  await pending;
  assert.equal(f.closed(), 1);
  assert.deepEqual(f.states, ['awaiting_user']);
});

test('worker abort and closing the page fail closed even before a user claims', async () => {
  for (const trigger of ['abort', 'page-close']) {
    const f = fixture();
    const controller = new AbortController();
    const pending = assert.rejects(f.handoff.request({ ...f, signal: controller.signal }), /did not complete/);
    if (trigger === 'abort') controller.abort();
    else f.page.emit('close');
    await pending;
    assert.equal(f.closed(), 1);
    assert.deepEqual(f.states, ['awaiting_user']);
  }
});

test('waiting for an unclaimed handoff also has a bounded lifetime', async () => {
  const f = fixture({ timeoutMs: 1000 });
  // Keep the test alive; production has the worker/session lifecycle timers.
  const keepAlive = setTimeout(() => {}, 2000);
  try {
    await assert.rejects(f.handoff.request(f), /did not complete/);
    assert.equal(f.closed(), 1);
    assert.deepEqual(f.states, ['awaiting_user']);
  } finally { clearTimeout(keepAlive); }
});

test('failure to publish the handoff closes the browser without an unhandled rejection', async () => {
  const f = fixture({ onState: () => { throw new Error('status unavailable'); } });
  await assert.rejects(f.handoff.request(f), /did not complete/);
  assert.equal(f.closed(), 1);
});
