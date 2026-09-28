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
