import { chromium } from 'playwright-core';

const MEET_PATH = /^\/[a-z]{3}-[a-z]{4}-[a-z]{3}\/?$/;
const ZOOM_PATH = /^\/j\/[0-9]{9,11}\/?$/;
const TEAMS_PATH = /^\/l\/meetup-join\/[^/]{10,512}\/[^/]{1,80}\/?$/;

export function meetingPlatform(value) {
  let url;
  try { url = new URL(value); } catch { throw new Error('a direct meeting link is required'); }
  if (url.protocol !== 'https:' || url.port || url.username || url.password || url.hash || url.href.length > 2048) {
    throw new Error('a direct HTTPS meeting link is required');
  }
  if (url.hostname === 'meet.google.com' && MEET_PATH.test(url.pathname) && !url.search) return 'meet';
  if ((url.hostname === 'zoom.us' || /^[a-z0-9-]+\.zoom\.us$/.test(url.hostname)) &&
      ZOOM_PATH.test(url.pathname) && [...url.searchParams.keys()].every(key => key === 'pwd')) return 'zoom';
  if (url.hostname === 'teams.microsoft.com' && TEAMS_PATH.test(url.pathname) &&
      [...url.searchParams.keys()].every(key => key === 'context')) return 'teams';
  throw new Error('a direct Google Meet, Zoom, or Teams meeting link is required');
}

export function meetingURL(value) {
  meetingPlatform(value);
  return new URL(value).toString();
}

async function visible(locator) {
  try { return await locator.isVisible(); } catch { return false; }
}

async function enableMicrophone(page, platform) {
  if (platform === 'zoom') {
    const joinAudio = page.getByRole('button', { name: /^Join Audio$/i });
    if (await visible(joinAudio)) await joinAudio.click();
    const computerAudio = page.getByRole('button', { name: /^Join with Computer Audio$/i });
    if (await visible(computerAudio)) await computerAudio.click();
  }
  const unmute = page.getByRole('button', {
    name: /^(Unmute(?: microphone)?|Turn on microphone|Mic button muted)$/i,
  });
  if (await visible(unmute)) await unmute.click();
}

async function joinGoogleMeet(page, displayName, timeoutMs, onAdmissionRequested) {
  const guestName = page.getByRole('textbox', { name: /^(Your name|Name)$/i });
  if (await visible(guestName)) await guestName.fill(displayName);
  const muted = page.getByRole('button', { name: /^Turn on microphone$/i });
  if (await visible(muted)) await muted.click();
  const join = page.getByRole('button', { name: /^(Join now|Ask to join)$/i });
  await join.waitFor({ timeout: timeoutMs });
  const admissionRequired = await visible(page.getByRole('button', { name: /^Ask to join$/i }));
  await join.click();
  if (admissionRequired) onAdmissionRequested?.();
  const leave = page.getByRole('button', { name: /^(Leave call|Leave meeting)$/i });
  await leave.waitFor({ timeout: timeoutMs });
  await enableMicrophone(page, 'meet');
  return leave;
}

async function joinZoom(page, displayName, timeoutMs, onAdmissionRequested) {
  const browserLink = page.getByRole('link', { name: /join from (your )?browser/i });
  await browserLink.waitFor({ timeout: timeoutMs });
  await browserLink.click();
  const name = page.getByRole('textbox', { name: /^(Your name|Name)$/i });
  if (await visible(name)) await name.fill(displayName);
  const join = page.getByRole('button', { name: /^Join$/i });
  await join.waitFor({ timeout: timeoutMs });
  await join.click();
  const leave = page.getByRole('button', { name: /^Leave( meeting)?$/i });
  if (await visible(page.getByText(/waiting for (the )?host|please wait.*admit/i))) onAdmissionRequested?.();
  await leave.waitFor({ timeout: timeoutMs });
  await enableMicrophone(page, 'zoom');
  return leave;
}

async function joinTeams(page, displayName, timeoutMs, onAdmissionRequested) {
  const browser = page.getByRole('button', { name: /continue on this browser|join on the web/i });
  if (await visible(browser)) await browser.click();
  const name = page.getByRole('textbox', { name: /^(Type your name|Enter your name|Name)$/i });
  if (await visible(name)) await name.fill(displayName);
  const join = page.getByRole('button', { name: /^Join now$/i });
  await join.waitFor({ timeout: timeoutMs });
  await join.click();
  const leave = page.getByRole('button', { name: /^Leave( meeting)?$/i });
  if (await visible(page.getByText(/let you in|waiting in the lobby/i))) onAdmissionRequested?.();
  await leave.waitFor({ timeout: timeoutMs });
  await enableMicrophone(page, 'teams');
  return leave;
}

export async function joinMeeting({ url, profileDir, executablePath, displayName = 'Axiom Agent', timeoutMs = 120000,
  chromiumAPI = chromium, signal, onAdmissionRequested }) {
  if (!profileDir) throw new Error('a browser profile directory is required');
  const target = meetingURL(url);
  const platform = meetingPlatform(target);
  const context = await chromiumAPI.launchPersistentContext(profileDir, {
    executablePath,
    headless: true,
    permissions: ['microphone'],
    args: ['--no-sandbox', '--disable-dev-shm-usage', '--use-fake-ui-for-media-stream',
      '--autoplay-policy=no-user-gesture-required'],
  });
  const abort = () => { void context.close().catch(() => {}); };
  if (signal?.aborted) abort();
  else signal?.addEventListener('abort', abort, { once: true });
  try {
    const page = context.pages()[0] ?? await context.newPage();
    await page.goto(target, { waitUntil: 'domcontentloaded', timeout: timeoutMs });
    const leave = platform === 'meet' ? await joinGoogleMeet(page, displayName, timeoutMs, onAdmissionRequested)
      : platform === 'zoom' ? await joinZoom(page, displayName, timeoutMs, onAdmissionRequested)
        : await joinTeams(page, displayName, timeoutMs, onAdmissionRequested);
    return {
      context, page, platform,
      async isPresent() { return visible(leave); },
      async leave() {
        try { await leave.click({ timeout: 5000 }); }
        catch { /* The host can end the call before teardown. */ }
        finally {
          signal?.removeEventListener('abort', abort);
          await context.close();
        }
      },
    };
  } catch (error) {
    signal?.removeEventListener('abort', abort);
    await context.close();
    throw error;
  }
}

export const joinMeet = joinMeeting;
