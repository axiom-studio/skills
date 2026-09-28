import test from 'node:test';
import assert from 'node:assert/strict';
import { browserIntervention, detectBrowserIntervention } from './browser-intervention.mjs';

function pageWith({ textbox, password = false, challenge = false, signInLink = false } = {}) {
  const locator = shown => ({ first() { return this; }, async isVisible() { return shown; } });
  return {
    getByRole: (role, { name }) => locator(role === 'textbox' ? Boolean(textbox && name.test(textbox)) : signInLink),
    locator: selector => locator(selector.includes('password') ? password : challenge),
  };
}

test('detects visible human-only gates without reading input values', async () => {
  for (const [state, reason] of [
    [{ textbox: 'Verification code', password: true }, 'verification'],
    [{ challenge: true }, 'challenge'],
    [{ password: true }, 'authentication'],
    [{ textbox: 'Email or phone' }, 'authentication'],
  ]) assert.equal(await detectBrowserIntervention(pageWith(state)), reason);
});

test('a normal page, sign-in navigation or inaccessible browser is not a human blocker', async () => {
  assert.equal(await detectBrowserIntervention(pageWith()), undefined);
  assert.equal(await detectBrowserIntervention(pageWith({ signInLink: true })), undefined);
  assert.equal(await detectBrowserIntervention({}), undefined);
});

test('handoff suggestions use fixed explanations and reject arbitrary page content', () => {
  assert.deepEqual(browserIntervention('authentication'), {
    type: 'browser_handoff', reason: 'authentication',
    summary: 'I need you to sign in before I can continue.', actionLabel: 'Take control',
  });
  assert.throws(() => browserIntervention('secret page text'), /Unsupported/);
  assert.equal(browserIntervention('manual_confirmation').type, 'browser_handoff');
});
