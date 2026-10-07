import test from 'node:test';
import assert from 'node:assert/strict';
import { launchCamoufox } from './camoufox-browser.mjs';

test('Camoufox uses a separate persistent profile and real audio without Chromium flags', async () => {
  let configured;
  const expected = {};
  const result = await launchCamoufox('/profile/tenant-agent', { display: ':17', audio: { sink: 'lb_1_capture', source: 'lb_1_source' } }, {
    prepareOptions: async options => { configured = options; return { env: options.env, executablePath: '/opt/camoufox/camoufox' }; },
    browserType: { launchPersistentContext: async (profile, options) => {
      assert.equal(profile, '/profile/tenant-agent/camoufox');
      assert.equal(options.viewport, null);
      assert.equal(options.acceptDownloads, false);
      assert.equal(options.env.DISPLAY, ':17');
      assert.equal(options.env.PULSE_SINK, 'lb_1_capture');
      assert.equal(options.env.PULSE_SOURCE, 'lb_1_source');
      assert.equal(options.args, undefined);
      assert.equal(options.permissions, undefined);
      return expected;
    } },
  });
  assert.equal(result, expected);
  assert.equal(configured.headless, false);
  assert.equal(configured.humanize, false);
  assert.equal(configured.block_webrtc, false);
  assert.equal(configured.firefox_user_prefs['media.navigator.streams.fake'], false);
  assert.equal(configured.firefox_user_prefs['permissions.default.microphone'], 1);
  assert.equal(configured.firefox_user_prefs['permissions.default.camera'], 2);
  assert.deepEqual(configured.exclude_addons, ['UBO']);
});

test('invalid private display or missing profile fails before browser preparation', async () => {
  const options = { prepareOptions: () => { throw new Error('must not run'); } };
  await assert.rejects(launchCamoufox('', {}, options), /profile or display/);
  await assert.rejects(launchCamoufox('/profile', { display: 'remote:0' }, options), /profile or display/);
  await assert.rejects(launchCamoufox('/profile', { audio: { sink: 'x;rm', source: 'ok' } }, options), /profile or display/);
});
