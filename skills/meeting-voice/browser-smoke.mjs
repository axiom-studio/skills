import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium } from 'playwright-core';
import { joinMeet } from './meet.mjs';

const profileDir = await mkdtemp(join(tmpdir(), 'axiom-meet-browser-smoke-'));
let meeting;
let admissionRequests = 0;
try {
  const chromiumAPI = {
    async launchPersistentContext(...args) {
      const context = await chromium.launchPersistentContext(...args);
      await context.route('https://meet.google.com/**', route => route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: `<!doctype html><html><body>
          <input aria-label="Your name">
          <button aria-label="Turn on microphone" onclick="window.microphoneEnabled=true">Turn on microphone</button>
          <button aria-label="Ask to join" onclick="window.joinRequested=true;document.body.insertAdjacentHTML('beforeend','<button aria-label=&quot;Leave call&quot;>Leave call</button>')">Ask to join</button>
        </body></html>`,
      }));
      await context.route('https://zoom.us/**', route => route.fulfill({
        status: 200, contentType: 'text/html',
        body: `<!doctype html><html><body>
          <a href="#" onclick="document.body.insertAdjacentHTML('beforeend','<input aria-label=&quot;Your name&quot;><button aria-label=&quot;Join&quot; onclick=&quot;document.body.insertAdjacentHTML(\\'beforeend\\',\\'<button aria-label=Leave>Leave</button>\\')&quot;>Join</button>')">Join from Your Browser</a>
        </body></html>`,
      }));
      await context.route('https://teams.microsoft.com/**', route => route.fulfill({
        status: 200, contentType: 'text/html',
        body: `<!doctype html><html><body>
          <button aria-label="Continue on this browser">Continue on this browser</button>
          <input aria-label="Type your name">
          <button aria-label="Join now" onclick="document.body.insertAdjacentHTML('beforeend','<button aria-label=&quot;Leave&quot;>Leave</button>')">Join now</button>
        </body></html>`,
      }));
      return context;
    },
  };
  meeting = await joinMeet({
    url: 'https://meet.google.com/abc-defg-hij',
    profileDir,
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    displayName: 'Axiom Test Agent',
    chromiumAPI,
    timeoutMs: 15000,
    onAdmissionRequested: () => { admissionRequests++; },
  });
  assert.equal(admissionRequests, 1);
  const state = await meeting.page.evaluate(async () => {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    const microphoneTracks = stream.getAudioTracks().length;
    stream.getTracks().forEach(track => track.stop());
    return {
      displayName: document.querySelector('input')?.value,
      microphoneEnabled: window.microphoneEnabled === true,
      joinRequested: window.joinRequested === true,
      microphoneTracks,
    };
  });
  assert.deepEqual(state, {
    displayName: 'Axiom Test Agent', microphoneEnabled: true,
    joinRequested: true, microphoneTracks: 1,
  });
  await meeting.page.evaluate(() => localStorage.setItem('axiom-meet-browser-smoke', 'retained'));
  await meeting.leave();
  meeting = undefined;
  meeting = await joinMeet({
    url: 'https://meet.google.com/abc-defg-hij',
    profileDir,
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    displayName: 'Axiom Test Agent',
    chromiumAPI,
    timeoutMs: 15000,
    onAdmissionRequested: () => { admissionRequests++; },
  });
  assert.equal(admissionRequests, 2);
  const retainedProfile = await meeting.page.evaluate(() => localStorage.getItem('axiom-meet-browser-smoke') === 'retained');
  assert.equal(retainedProfile, true);
  await meeting.leave();
  meeting = undefined;
  meeting = await joinMeet({ url: 'https://zoom.us/j/12345678901', profileDir,
    executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium', displayName: 'Axiom Test Agent',
    chromiumAPI, timeoutMs: 15000 });
  assert.equal(meeting.platform, 'zoom');
  assert.equal(await meeting.page.locator('input').inputValue(), 'Axiom Test Agent');
  await meeting.leave();
  meeting = undefined;
  meeting = await joinMeet({ url: 'https://teams.microsoft.com/l/meetup-join/19%3ameeting_abc%40thread.v2/0',
    profileDir, executablePath: process.env.CHROMIUM_PATH || '/usr/bin/chromium',
    displayName: 'Axiom Test Agent', chromiumAPI, timeoutMs: 15000 });
  assert.equal(meeting.platform, 'teams');
  assert.equal(await meeting.page.locator('input').inputValue(), 'Axiom Test Agent');
  console.log(JSON.stringify({ ...state, admissionRequests, retainedProfile, zoom: true, teams: true }));
} finally {
  if (meeting) await meeting.leave();
  await rm(profileDir, { recursive: true, force: true });
}
