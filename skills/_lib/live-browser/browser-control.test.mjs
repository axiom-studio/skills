import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BrowserControl } from './browser-control.mjs';

const owner = { tenantID: 'tenant-a', agentID: 'agent-a', userID: 'human-a' };
function setup() {
  let time = 1000;
  let closed = 0;
  const control = new BrowserControl({ ...owner, close: async () => { closed++; }, now: () => time });
  return { control, expire: () => { time += 700000; }, closed: () => closed };
}

test('human takeover excludes automation and explicit return revokes old control', async () => {
  const { control } = setup();
  await control.automate(async () => {});
  const lease = await control.takeOver(owner);
  await assert.rejects(control.automate(() => assert.fail('agent acted during takeover')), /paused/);
  assert.equal(await control.human(owner, lease.id, async () => 'input delivered'), 'input delivered');
  await control.returnControl(owner, lease.id);
  await assert.rejects(control.human(owner, lease.id, () => assert.fail()), /denied/);
  assert.equal(await control.automate(async () => 'same browser'), 'same browser');
  await control.close();
});

test('takeover waits for in-flight automation and blocks a second controller', async () => {
  const { control } = setup();
  let finish;
  const running = control.automate(() => new Promise(resolve => { finish = resolve; }));
  await Promise.resolve();
  const takeover = control.takeOver(owner);
  assert.equal(control.state, 'transferring');
  await assert.rejects(control.takeOver(owner), /reserved/);
  finish();
  await running;
  await takeover;
  assert.equal(control.state, 'human');
  await control.close();
});

test('foreign tenants, agents and users cannot control or resume a browser', async () => {
  const { control } = setup();
  await assert.rejects(control.takeOver({ ...owner, tenantID: 'tenant-b' }), /denied/);
  const lease = await control.takeOver(owner);
  for (const principal of [{ ...owner, tenantID: 'tenant-b' }, { ...owner, agentID: 'agent-b' },
    { ...owner, userID: 'human-b' }, { ...owner, userID: '' }]) {
    await assert.rejects(control.human(principal, lease.id, () => assert.fail()), /denied/);
    await assert.rejects(control.returnControl(principal, lease.id), /denied/);
  }
  await control.close();
});

test('expiry closes the browser and never automatically resumes the agent', async () => {
  const { control, expire, closed } = setup();
  const lease = await control.takeOver(owner);
  expire();
  await assert.rejects(control.human(owner, lease.id, () => assert.fail()), /expired/);
  assert.equal(control.state, 'closed');
  assert.equal(closed(), 1);
  await assert.rejects(control.automate(() => assert.fail()), /paused/);
  await control.close();
  assert.equal(closed(), 1);
});

test('closing during a pending takeover cannot revive browser access', async () => {
  const { control } = setup();
  let finish;
  const running = control.automate(() => new Promise(resolve => { finish = resolve; }));
  await Promise.resolve();
  const takeover = control.takeOver(owner);
  await control.close();
  finish();
  await running;
  await assert.rejects(takeover, /closed/);
  assert.equal(control.state, 'closed');
});

function pausable() {
  let time = 1000;
  let closed = 0;
  const control = new BrowserControl({ ...owner, onExpire: 'pause', close: async () => { closed++; }, now: () => time });
  return { control, advance: ms => { time += ms; }, closed: () => closed };
}

test('pausable control lets a human claim while automating and after an agent pause', async () => {
  const { control } = pausable();
  let finish;
  const running = control.automate(() => new Promise(resolve => { finish = resolve; }));
  await Promise.resolve();
  const takeover = control.takeOver(owner);
  finish();
  await running;
  const lease = await takeover;
  assert.equal(control.state, 'human');
  assert.equal(control.lease.expiresAt, lease.expiresAt);
  assert.equal(Object.hasOwn(control.lease, 'id'), false);
  await control.returnControl(owner, lease.id);
  await control.pause();
  assert.equal(control.state, 'paused');
  await assert.rejects(control.automate(() => assert.fail()), /paused/);
  const again = await control.takeOver(owner);
  await control.returnControl(owner, again.id);
  assert.equal(control.state, 'automation');
  await control.close();
});

test('pausable lease expiry pauses without closing or resuming the agent', async () => {
  const { control, advance, closed } = pausable();
  const lease = await control.takeOver(owner, 1000);
  advance(2000);
  await assert.rejects(control.human(owner, lease.id, () => assert.fail()), /expired/);
  assert.equal(control.state, 'paused');
  assert.equal(closed(), 0);
  await assert.rejects(control.automate(() => assert.fail()), /paused/);
  await control.close();
  assert.equal(closed(), 1);
});

test('settled waits for an explicit return and is bounded by its timeout', async () => {
  const control = new BrowserControl({ ...owner, onExpire: 'pause', close: async () => {} });
  const lease = await control.takeOver(owner);
  assert.equal(await control.settled(20), 'human');
  const waiting = control.settled(5000);
  await control.returnControl(owner, lease.id);
  assert.equal(await waiting, 'automation');
  await assert.rejects(new BrowserControl({ ...owner, close: async () => {} }).pause(), /cannot pause/);
  await control.close();
});
