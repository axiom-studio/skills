import test from 'node:test';
import assert from 'node:assert/strict';
import { EventEmitter } from 'node:events';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { guardPlaywrightNullResponse, isPlaywrightNullResponse } from './playwright-guard.mjs';

const require = createRequire(import.meta.url);
const { FFNetworkManager } = require(join(dirname(require.resolve('playwright-core')), 'lib/server/firefox/ffNetworkManager.js'));

// The error a refused TURN-over-TLS channel causes, thrown by playwright-core
// itself: Firefox reports requestFinished for a request with no response.
function nullResponseError() {
  const manager = Object.create(FFNetworkManager.prototype);
  manager._requests = new Map([['15', { request: { _existingResponse: () => null } }]]);
  try { manager._onRequestFinished({ requestId: '15', transferSize: 39, encodedBodySize: 0 }); } catch (error) { return error; }
  assert.fail('playwright-core no longer throws on a finished request without a response; review the guard');
}

function guarded() {
  const target = new EventEmitter();
  const logged = { warn: [], error: [] };
  const exits = [];
  const remove = guardPlaywrightNullResponse({ target, exit: code => exits.push(code),
    log: { warn: line => logged.warn.push(line), error: error => logged.error.push(error) } });
  return { target, logged, exits, remove };
}

test('the guard swallows the null-response TypeError from playwright-core and logs one event', () => {
  const error = nullResponseError();
  assert.ok(isPlaywrightNullResponse(error));
  const { target, logged, exits, remove } = guarded();
  target.emit('uncaughtException', error, 'uncaughtException');
  assert.deepEqual(exits, []);
  assert.deepEqual(logged.warn, ['{"event":"live_browser_playwright_null_response"}']);
  assert.deepEqual(logged.error, []);
  remove();
  assert.equal(target.listenerCount('uncaughtException'), 0);
});

test('every other uncaught error still ends the process', () => {
  const real = nullResponseError();
  const sameMessageElsewhere = new TypeError(real.message);
  const otherFrame = new TypeError(real.message);
  otherFrame.stack = real.stack.replace('_onRequestFinished', '_onResponseReceived');
  const otherFile = new TypeError(real.message);
  otherFile.stack = real.stack.replace('ffNetworkManager.js', 'ffPage.js');
  const otherMessage = new TypeError("Cannot read properties of null (reading 'status')");
  otherMessage.stack = real.stack.replace("'setTransferSize'", "'status'");
  const notTypeError = new Error(real.message);
  notTypeError.stack = real.stack.replace(/^TypeError/, 'Error');
  for (const error of [sameMessageElsewhere, otherFrame, otherFile, otherMessage, notTypeError, new Error('boom'), 'thrown string']) {
    assert.equal(isPlaywrightNullResponse(error), false, String(error?.stack ?? error));
    const { target, logged, exits } = guarded();
    target.emit('uncaughtException', error, 'uncaughtException');
    assert.deepEqual(exits, [1]);
    assert.deepEqual(logged.warn, []);
    assert.equal(logged.error.length, 1);
  }
});
