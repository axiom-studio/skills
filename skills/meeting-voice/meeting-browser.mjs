import { firefox } from 'playwright-core';
import { launchOptions } from 'camoufox-js';
import { join } from 'node:path';

// Engine-specific profiles stay inside the existing tenant/agent profile root.
// Chromium cookies and locks are never imported or reused by Firefox.
export async function launchMeetingBrowser(profileRoot, { display } = {}, {
  prepareOptions = launchOptions, browserType = firefox,
} = {}) {
    if (!profileRoot || (display !== undefined && !/^:[0-9]{1,5}$/.test(display))) {
      throw new Error('Browser profile or display is unavailable');
    }
    const options = await prepareOptions({
      os: 'linux', headless: !display, window: [1280, 800],
      humanize: false, block_webrtc: false, geoip: false,
      // No runtime extension downloads or third-party network setup calls.
      exclude_addons: ['UBO'],
      env: { ...process.env, ...(display ? { DISPLAY: display, MOZ_ENABLE_WAYLAND: '0' } : {}) },
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

export const meetingBrowser = { launchPersistentContext: launchMeetingBrowser };
