import assert from 'node:assert/strict';
import { test } from 'node:test';
import { BrowserHandoff, BrowserPausedError } from './browser-handoff.mjs';
import { BrowserHumanView } from './browser-human-view.mjs';

const principal = { tenantID: 'tenant-1', agentID: 'agent-1', userID: 'human-1' };

function fixture(options = {}) {
  const states = [];
  let closed = 0;
  const page = {
    viewportSize: () => ({ width: 1280, height: 800 }),
    screenshot: async () => Buffer.from('private screen'),
    keyboard: { insertText: async () => {} },
  };
  const handoff = new BrowserHandoff({ ...principal, onState: state => states.push(state),
    close: async () => { closed++; }, inputFactory: ({ control }) => new BrowserHumanView({ page, control }), ...options });
  return { handoff, page, states, closed: () => closed };
}

test('a human may claim while the agent automates; claim waits for the in-flight action', async () => {
  const f = fixture();
  assert.equal(f.handoff.status, 'automating');
  let finish;
  const running = f.handoff.automate(() => new Promise(resolve => { finish = resolve; }));
  await Promise.resolve();
  const claim = f.handoff.handle(principal, { type: 'claim' });
  assert.equal(f.handoff.status, 'human');
  const blocked = assert.rejects(f.handoff.automate(() => assert.fail('agent acted during takeover')), BrowserPausedError);
  finish('done');
  await blocked;
  assert.equal(await running, 'done');
  const lease = await claim;
  assert.ok(f.handoff.lease.expiresAt >= Date.now());
  const screen = await f.handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'frame' } });
  assert.equal(screen.bytes.toString(), 'private screen');
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  assert.equal(f.handoff.status, 'automating');
  assert.equal(await f.handoff.automate(async () => 'same page'), 'same page');
  assert.deepEqual(f.states, ['human', 'automating']);
  assert.equal(f.closed(), 0);
  await f.handoff.close();
  assert.equal(f.handoff.status, 'none');
});

test('agent-requested handoff pauses automation until the lease owner hands back', async () => {
  const f = fixture();
  const intervention = await f.handoff.requestHandoff('payment', 'Pay for the selected flight');
  assert.deepEqual(intervention, { type: 'browser_handoff', reason: 'payment', summary: 'Pay for the selected flight', actionLabel: 'Take control' });
  assert.equal(f.handoff.status, 'awaiting_user');
  assert.deepEqual(f.handoff.intervention, intervention);
  await assert.rejects(f.handoff.automate(() => assert.fail()), error => error.status === 'awaiting_user');
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  assert.equal(f.handoff.intervention, undefined);
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  assert.equal(f.handoff.status, 'automating');
  assert.equal(f.handoff.intervention, undefined);
  await f.handoff.close();
});

test('another user cannot claim, read, cancel, or resume a reserved browser', async () => {
  const f = fixture();
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  for (const command of [
    { type: 'claim' }, { type: 'input', leaseID: lease.id, input: { type: 'frame' } },
    { type: 'cancel', leaseID: lease.id }, { type: 'resume', leaseID: lease.id },
  ]) await assert.rejects(f.handoff.handle({ ...principal, userID: 'other-user' }, command), /could not be completed/);
  for (const command of [null, [], { type: 'claim', leaseID: 'x' }, { type: 'resume', leaseID: lease.id, input: {} }, { type: 'unknown', leaseID: lease.id }]) {
    await assert.rejects(f.handoff.handle(principal, command), /could not be completed/);
  }
  assert.equal(f.closed(), 0);
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  await f.handoff.close();
});

test('cancel stops the browser task and never resumes automation', async () => {
  const f = fixture();
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  await f.handoff.handle(principal, { type: 'cancel', leaseID: lease.id });
  assert.equal(f.closed(), 1);
  assert.equal(f.handoff.status, 'none');
  await assert.rejects(f.handoff.automate(() => assert.fail()));
  await assert.rejects(f.handoff.handle(principal, { type: 'claim' }), /could not be completed/);
});

test('lease expiry pauses the agent instead of closing the page or resuming', async () => {
  let time = 10000;
  const f = fixture({ leaseMs: 1000, now: () => time });
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  time += 2000;
  await assert.rejects(f.handoff.handle(principal, { type: 'input', leaseID: lease.id, input: { type: 'frame' } }));
  assert.equal(f.handoff.status, 'awaiting_user');
  assert.equal(f.handoff.intervention.reason, 'other');
  assert.equal(f.closed(), 0);
  const again = await f.handoff.handle(principal, { type: 'claim' });
  await f.handoff.handle(principal, { type: 'resume', leaseID: again.id });
  assert.equal(f.handoff.status, 'automating');
  await f.handoff.close();
});

test('watch is lease-free, view-only and bounded; lease video requires the owner', async () => {
  const signals = [];
  const f = fixture({ maxWatchers: 1, videoFactory: ({ signal }) => {
    signals.push(signal);
    return { close: () => {}, async *[Symbol.asyncIterator]() {
      yield Buffer.from('encoded-video');
      await new Promise(resolve => signal.addEventListener('abort', resolve, { once: true }));
    } };
  } });
  const watch = f.handoff.watch();
  assert.equal((await watch.next()).value.toString(), 'encoded-video');
  await assert.rejects(f.handoff.watch().next(), /unavailable/);
  await assert.rejects(f.handoff.stream(principal, 'no-lease').next(), /unavailable/);
  const lease = await f.handoff.handle(principal, { type: 'claim' });
  await assert.rejects(f.handoff.stream({ ...principal, userID: 'another-human' }, lease.id).next(), /unavailable/);
  const leased = f.handoff.stream(principal, lease.id);
  assert.equal((await leased.next()).value.toString(), 'encoded-video');
  const pendingLeased = leased.next();
  // Watching continues while a human holds control.
  const pendingWatch = watch.next();
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  assert.equal((await pendingLeased).done, true);
  await f.handoff.close();
  assert.equal((await pendingWatch).done, true);
  assert.ok(signals.every(signal => signal.aborted));
  await assert.rejects(f.handoff.watch().next(), /unavailable/);
});

test('desktop teardown during explicit return cannot cancel the resumed browser', async () => {
  let stopped, lease, transportClosed = false;
  const f = fixture({ desktopFactory: () => ({
    write: async () => {},
    async close() {
      transportClosed = true; stopped?.();
      await assert.rejects(f.handoff.handle(principal, { type: 'cancel', leaseID: lease.id }));
    },
    async *[Symbol.asyncIterator]() {
      yield Buffer.from('RFB');
      await new Promise(resolve => { stopped = resolve; });
    },
  }) });
  lease = await f.handoff.handle(principal, { type: 'claim' });
  const stream = f.handoff.stream(principal, lease.id, { desktop: true });
  await stream.next(); const next = stream.next();
  await assert.rejects(f.handoff.writeDesktop({ ...principal, tenantID: 'foreign' }, lease.id, Buffer.from('key')));
  await f.handoff.writeDesktop(principal, lease.id, Buffer.from('key'));
  await f.handoff.handle(principal, { type: 'resume', leaseID: lease.id });
  await next;
  assert.equal(transportClosed, true); assert.equal(f.closed(), 0);
  assert.equal(f.handoff.status, 'automating');
  await f.handoff.close();
});

test('state callbacks carry the intervention, including when lease expiry pauses the agent', async () => {
  let time = 10000;
  const seen = [];
  const f = fixture({ leaseMs: 1000, now: () => time, onState: (state, intervention) => seen.push([state, intervention?.reason]) });
  await f.handoff.handle(principal, { type: 'claim' });
  time += 2000;
  assert.equal(await f.handoff.settled(10), 'awaiting_user');
  assert.deepEqual(seen, [['human', undefined], ['awaiting_user', 'other']]);
  await f.handoff.close();
});
