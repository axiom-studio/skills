import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { BrowserControl } from './browser-control.mjs';
import { BrowserDesktopInput } from './browser-desktop-input.mjs';
import { createBrowserDesktop } from './browser-desktop.mjs';
import { meetingBrowser } from './meeting-browser.mjs';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const owner = { tenantID: 't', agentID: 'a', userID: 'human' };
async function setup() {
  const calls = [];
  const control = new BrowserControl({ ...owner, close: async () => {} });
  const lease = await control.takeOver(owner);
  const view = new BrowserDesktopInput({ display: ':17', control, signal: new AbortController().signal,
    spawnProcess(command, args, options) {
      const child = new EventEmitter(); child.stdin = new PassThrough(); child.kill = () => {};
      const call = { command, args, options, text: '' }; calls.push(call);
      child.stdin.on('data', data => { call.text += data; });
      child.stdin.on('finish', () => queueMicrotask(() => child.emit('exit', 0)));
      return child;
    } });
  return { calls, control, lease, view };
}

test('direct input uses the private display and never places typed secrets in arguments', async () => {
  const { calls, control, lease, view } = await setup();
  try {
    for (const input of [{ type: 'pointer', action: 'down', x: 24, y: 80, button: 0 },
      { type: 'pointer', action: 'move', x: 120, y: 80, button: 0 },
      { type: 'pointer', action: 'up', x: 120, y: 80, button: 0 },
      { type: 'text', text: 'private test text' }, { type: 'key', key: 'Enter' },
      { type: 'scroll', deltaY: 250 }]) {
      assert.deepEqual(await view.dispatch(owner, lease.id, input), { type: 'ack' });
    }
    assert.deepEqual(calls[0].args, ['mousemove', '24', '80', 'mousedown', '1']);
    assert.deepEqual(calls[3].args, ['type', '--clearmodifiers', '--delay', '0', '--file', '-']);
    assert.equal(calls[3].text, 'private test text');
    assert.ok(calls.every(call => call.options.env.DISPLAY === ':17' && call.options.shell === undefined));
    await view.release();
    assert.deepEqual(calls.at(-1).args, ['mouseup', '1', 'mouseup', '2', 'mouseup', '3']);
  } finally { await control.close(); }
});

test('foreign principals and malformed input cannot invoke native processes', async () => {
  const { calls, control, lease, view } = await setup();
  try {
    await assert.rejects(view.dispatch({ ...owner, userID: 'other' }, lease.id, { type: 'key', key: 'Enter' }));
    for (const input of [null, { type: 'frame' }, { type: 'text', text: 'a\nb' },
      { type: 'key', key: '__proto__' }, { type: 'key', key: 'Control+Shift+J' },
      { type: 'pointer', action: 'down', x: 1280, y: 1, button: 0 },
      { type: 'pointer', action: 'down', x: 1, y: 1, button: 4 },
      { type: 'text', text: 'x', command: 'anything' }, { type: 'scroll', deltaY: Infinity }]) {
      await assert.rejects(view.dispatch(owner, lease.id, input), /could not be completed/);
    }
    assert.equal(calls.length, 0);
  } finally { await control.close(); }
});

test('native clicks and text reach the visible browser on the isolated video display',
  { skip: process.env.BROWSER_VIDEO_INTEGRATION !== '1', timeout: 20000 }, async () => {
    const controller = new AbortController();
    const profile = await mkdtemp(join(tmpdir(), 'browser-input-test-'));
    let display, context, control;
    try {
      display = await createBrowserDesktop({ signal: controller.signal });
      context = await meetingBrowser.launchPersistentContext(profile, { display: display.display });
      const page = context.pages()[0];
      await page.setContent('<input aria-label="Test input" style="position:absolute;left:30px;top:40px;width:400px;height:40px"><div style="height:2400px"></div>');
      await page.bringToFront();
      await page.waitForFunction(() => document.hasFocus(), undefined, { timeout: 3000 });
      // Allow the freshly mapped native window to finish its startup animation.
      // This is test setup only; production input is never delayed or retried.
      await page.waitForTimeout(1000);
      await page.evaluate(() => document.addEventListener('mousedown', event => {
        document.body.dataset.lastClick = JSON.stringify({ tag: event.target.tagName, x: event.clientX, y: event.clientY });
      }));
      const geometry = await page.evaluate(() => ({ width: outerWidth, height: outerHeight,
        contentOffset: outerHeight - innerHeight }));
      assert.equal(geometry.width, 1280);
      assert.equal(geometry.height, 800);
      control = new BrowserControl({ ...owner, close: async () => {} });
      const lease = await control.takeOver(owner);
      const view = new BrowserDesktopInput({ display: display.display, control, signal: controller.signal });
      for (const action of ['down', 'up']) await view.dispatch(owner, lease.id,
        { type: 'pointer', action, x: 100, y: 60 + geometry.contentOffset, button: 0 });
      await page.waitForFunction(() => document.body.dataset.lastClick, undefined, { timeout: 2000 });
      assert.match(await page.evaluate(() => document.body.dataset.lastClick), /"tag":"INPUT"/);
      await view.dispatch(owner, lease.id, { type: 'text', text: 'direct input works' });
      await page.waitForFunction(() => document.querySelector('input').value === 'direct input works', undefined, { timeout: 2000 });
      await view.dispatch(owner, lease.id, { type: 'scroll', deltaY: 500 });
      await page.waitForFunction(() => scrollY > 0);
      await view.release();
    } finally {
      await control?.close();
      await context?.close();
      controller.abort(); display?.close();
      await rm(profile, { recursive: true, force: true });
    }
  });
