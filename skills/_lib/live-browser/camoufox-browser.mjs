import { firefox } from 'playwright-core';
import { launchOptions } from 'camoufox-js';
import { join } from 'node:path';

const SINK = /^[a-z][a-z0-9_]{0,62}$/;

// Engine-specific profiles stay inside the caller's private profile root.
// Chromium cookies and locks are never imported or reused by Firefox. Audio
// routing (PULSE_SINK/PULSE_SOURCE) is per browser so concurrent sessions never
// hear or speak into each other's pages.
export async function launchCamoufox(profileRoot, { display, audio } = {}, {
  prepareOptions = launchOptions, browserType = firefox,
} = {}) {
  if (!profileRoot || (display !== undefined && !/^:[0-9]{1,5}$/.test(display)) ||
    (audio !== undefined && (!SINK.test(audio?.sink ?? '') || !SINK.test(audio?.source ?? '')))) {
    throw new Error('Browser profile or display is unavailable');
  }
  const options = await prepareOptions({
    os: 'linux', headless: !display, window: [1280, 800],
    humanize: false, block_webrtc: false, geoip: false,
    // No runtime extension downloads or third-party network setup calls.
    exclude_addons: ['UBO'],
    env: { ...process.env, ...(display ? { DISPLAY: display, MOZ_ENABLE_WAYLAND: '0' } : {}),
      ...(audio ? { PULSE_SINK: audio.sink, PULSE_SOURCE: audio.source } : {}) },
    firefox_user_prefs: {
      'media.navigator.streams.fake': false,
      'permissions.default.microphone': 1,
      'permissions.default.camera': 2,
      'media.autoplay.default': 0,
    },
  });
  return browserType.launchPersistentContext(join(profileRoot, 'camoufox'), {
    ...options, viewport: null, acceptDownloads: false,
  });
}

export const camoufoxBrowser = { launchPersistentContext: launchCamoufox };
