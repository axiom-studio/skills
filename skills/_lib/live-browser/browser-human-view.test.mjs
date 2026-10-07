import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BrowserControl } from './browser-control.mjs';
import { BrowserHumanView } from './browser-human-view.mjs';

const owner = { tenantID: 'tenant', agentID: 'agent', userID: 'human' };
async function setup() {
  const calls = [];
  const page = {
    viewportSize: () => ({ width: 1280, height: 800 }),
    screenshot: async options => { calls.push(['frame', options]); return Buffer.from('jpeg'); },
    mouse: { click: async (...args) => calls.push(['click', ...args]),
      wheel: async (...args) => calls.push(['scroll', ...args]) },
    keyboard: { insertText: async text => calls.push(['text', text]),
      press: async key => calls.push(['key', key]) },
  };
  const control = new BrowserControl({ ...owner, close: async () => {} });
  const lease = await control.takeOver(owner);
  return { page, calls, control, lease, view: new BrowserHumanView({ page, control }) };
}

test('human frames and input use the retained page under one exclusive lease', async () => {
  const { view, calls, control, lease } = await setup();
  try {
    const frame = await view.dispatch(owner, lease.id, { type: 'frame' });
    assert.equal(frame.mimeType, 'image/jpeg');
    assert.equal(frame.width, 1280);
    for (const input of [{ type: 'click', x: 20, y: 30 }, { type: 'text', text: 'user input' },
      { type: 'key', key: 'Tab' }, { type: 'scroll', deltaY: 200 }]) {
      assert.deepEqual(await view.dispatch(owner, lease.id, input), { type: 'ack' });
    }
    assert.deepEqual(calls.slice(1), [['click', 20, 30], ['text', 'user input'], ['key', 'Tab'], ['scroll', 0, 200]]);
    await control.returnControl(owner, lease.id);
    await assert.rejects(view.dispatch(owner, lease.id, { type: 'frame' }), /could not be completed/);
    assert.equal(calls.length, 5);
  } finally { await control.close(); }
});

test('foreign users cannot observe frames or send input', async () => {
  const { view, calls, control, lease } = await setup();
  try {
    for (const principal of [{ ...owner, tenantID: 'other' }, { ...owner, agentID: 'other' }, { ...owner, userID: 'other' }]) {
      await assert.rejects(view.dispatch(principal, lease.id, { type: 'frame' }));
      await assert.rejects(view.dispatch(principal, lease.id, { type: 'text', text: 'anything' }));
    }
    assert.deepEqual(calls, []);
  } finally { await control.close(); }
});

test('rejects unbounded input, browser shortcuts, unknown commands and hidden extra fields', async () => {
  const { view, calls, control, lease } = await setup();
  try {
    for (const input of [{ type: 'eval', script: 'anything' }, { type: 'navigate', url: 'https://example.com' },
      { type: 'key', key: 'Control+Shift+J' }, { type: 'click', x: -1, y: 0 },
      { type: 'click', x: 1280, y: 0 }, { type: 'click', x: 1.2, y: 0 },
      { type: 'text', text: 'a'.repeat(4097) }, { type: 'text', text: 'a\nb' },
      { type: 'frame', fullPage: true }, { type: 'scroll', deltaY: Infinity }, null]) {
      await assert.rejects(view.dispatch(owner, lease.id, input));
    }
    assert.deepEqual(calls, []);
  } finally { await control.close(); }
});

test('browser exceptions cannot echo a password into the response', async () => {
  const { view, page, control, lease } = await setup();
  try {
    page.keyboard.insertText = async text => { throw new Error(`failed typing ${text}`); };
    await assert.rejects(view.dispatch(owner, lease.id, { type: 'text', text: 'private-password' }), error => {
      assert.equal(error.message, 'Browser control request could not be completed');
      assert.equal(error.cause, undefined);
      assert.doesNotMatch(error.stack, /private-password/);
      return true;
    });
  } finally { await control.close(); }
});
