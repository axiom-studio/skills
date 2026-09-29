import { meetingBrowser } from './meeting-browser.mjs';
import { detectBrowserIntervention } from './browser-intervention.mjs';

const MEET_PATH = /^\/[a-z]{3}-[a-z]{4}-[a-z]{3}\/?$/;
const ZOOM_PATH = /^\/j\/[0-9]{9,11}\/?$/;
const TEAMS_PATH = /^\/l\/meetup-join\/[^/]{10,512}\/[^/]{1,80}\/?$/;

export class MeetingJoinError extends Error {
  constructor(stage) {
    super(`The meeting could not be joined: ${stage}. No active meeting was confirmed.`);
    this.name = 'MeetingJoinError';
  }
}

async function joinStep(stage, operation) {
  try { return await operation(); }
  catch (error) {
    if (error instanceof MeetingJoinError) throw error;
    throw new MeetingJoinError(stage);
  }
}

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
  const prejoin = page.getByRole('button', {
    name: /^(Join now|Ask to join|Continue without microphone(?: and camera)?)$/i,
  }).first();
  const refused = page.getByText("You can't join this video call", { exact: true });
  await joinStep('Google Meet did not show its prejoin controls', () => Promise.race([
    prejoin.waitFor({ timeout: timeoutMs }),
    refused.waitFor({ state: 'visible', timeout: timeoutMs }).then(() => {
      throw new MeetingJoinError('Google Meet refused access to this call; ask the host to confirm the link and guest access');
    }),
  ]));
  const withoutMedia = page.getByRole('button', { name: /^Continue without microphone(?: and camera)?$/i });
  if (await visible(withoutMedia)) {
    await joinStep('the Google Meet media setup dialog could not be dismissed', () => withoutMedia.click({ timeout: timeoutMs }));
  }
  const join = page.getByRole('button', { name: /^(Join now|Ask to join)$/i });
  await joinStep('Google Meet did not show its join controls', () => join.waitFor({ timeout: timeoutMs }));
  // Meet renders the guest form asynchronously after DOMContentLoaded.
  // Inspect the name field only once the prejoin controls are ready.
  const guestName = page.getByRole('textbox', { name: /^(Your name|Name)$/i });
  if (await visible(guestName)) await joinStep('the guest name could not be entered', () => guestName.fill(displayName));
  const muted = page.getByRole('button', { name: /^Turn on microphone$/i });
  if (await visible(muted)) await muted.click();
  const admissionRequired = await visible(page.getByRole('button', { name: /^Ask to join$/i }));
  await joinStep('the Google Meet join request could not be submitted', () => join.click({ timeout: timeoutMs }));
  if (admissionRequired) onAdmissionRequested?.();
  const leave = page.getByRole('button', { name: /^(Leave call|Leave meeting)$/i });
  await joinStep('Google Meet did not confirm admission before the timeout', () => leave.waitFor({ timeout: timeoutMs }));
  await enableMicrophone(page, 'meet');
  return leave;
}

async function joinZoom(page, displayName, timeoutMs, onAdmissionRequested) {
  const browserLink = page.getByRole('link', { name: /join from (your )?browser/i }).first();
  const join = page.getByRole('button', { name: /^Join$/i }).first();
  const leave = page.getByRole('button', { name: /^Leave( meeting)?$/i }).first();
  const waiting = page.getByText(/waiting for (the )?host|please wait.*(admit|let you in)|host will let you in/i).first();
  const connecting = page.getByText(/^(Joining(?: meeting)?|Connecting(?: to (?:the )?meeting)?)[.…]*$/i).first();
  const deadline = Date.now() + timeoutMs;
  let requested = false;
  let openedBrowser = false;
  let admissionReported = false;
  let stage = 'Zoom did not show recognizable join controls';
  while (Date.now() < deadline) {
    if (page.isClosed()) throw new MeetingJoinError('the Zoom browser was closed before admission');
    // A waiting room may also expose Leave. It is not proof of admission.
    if (await visible(waiting)) {
      requested = true;
      stage = 'Zoom did not confirm host admission before the timeout';
      if (!admissionReported) { admissionReported = true; onAdmissionRequested?.(); }
    } else if (await visible(connecting)) {
      requested = true;
      stage = 'Zoom remained on its connecting screen before the timeout';
    } else if (await visible(leave)) {
      await joinStep('Zoom audio setup could not be completed', () => enableMicrophone(page, 'zoom'));
      return leave;
    } else if (!requested && await visible(join)) {
      const name = page.getByRole('textbox', { name: /^(Your name|Name)$/i }).first();
      if (await visible(name)) await joinStep('the Zoom guest name could not be entered', () => name.fill(displayName, { timeout: Math.max(1, deadline - Date.now()) }));
      await joinStep('the Zoom join request could not be submitted', () => join.click({ timeout: Math.max(1, deadline - Date.now()) }));
      requested = true;
      stage = 'Zoom did not confirm admission after the join request';
    } else if (!requested && !openedBrowser && await visible(browserLink)) {
      await joinStep('the Zoom browser client could not be opened', () => browserLink.click({ timeout: Math.max(1, deadline - Date.now()) }));
      openedBrowser = true;
      stage = 'Zoom did not show its browser prejoin controls';
    }
    await new Promise(resolve => setTimeout(resolve, Math.min(200, Math.max(1, deadline - Date.now()))));
  }
  throw new MeetingJoinError(stage);
}

// Inspect only a Zoom route for this authorized meeting after private handoff.
// Never navigate away from an in-progress join or inspect a remaining login form.
function canResumeZoom(page, target) {
  try {
    const current = new URL(page.url());
    const meetingID = new URL(target).pathname.split('/')[2];
    return current.protocol === 'https:' && !current.port && !current.username && !current.password &&
      (current.hostname === 'zoom.us' || /^[a-z0-9-]+\.zoom\.us$/.test(current.hostname)) &&
      new RegExp(`^/(?:j/${meetingID}|wc/${meetingID}/(?:join|start)|wc/(?:join|start)/${meetingID})/?$`).test(current.pathname);
  } catch { return false; }
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

export async function joinMeeting({ url, profileDir, displayName = 'Axiom Agent', timeoutMs = 120000,
  browserAPI = meetingBrowser, signal, display, onAdmissionRequested, handoff, handoffBeforeJoin = false, interventionReason = detectBrowserIntervention }) {
  if (!profileDir) throw new Error('a browser profile directory is required');
  if (display !== undefined && !/^:[0-9]{1,5}$/.test(display)) throw new Error('Invalid browser display');
  const target = meetingURL(url);
  const platform = meetingPlatform(target);
  const context = await browserAPI.launchPersistentContext(profileDir, { display });
  const abort = () => { void context.close().catch(() => {}); };
  if (signal?.aborted) abort();
  else signal?.addEventListener('abort', abort, { once: true });
  try {
    const page = context.pages()[0] ?? await context.newPage();
    await joinStep('the meeting page could not be loaded', () => page.goto(target, { waitUntil: 'domcontentloaded', timeout: timeoutMs }));
    let leave;
    if (handoffBeforeJoin) {
      if (!handoff) throw new MeetingJoinError('human browser handoff is unavailable');
      await joinStep('browser handoff did not complete', () => handoff.request({ page, context, signal, reason: 'manual_confirmation' }));
      if (signal?.aborted) throw new MeetingJoinError('the meeting was cancelled');
      if (platform === 'zoom') {
        if (!canResumeZoom(page, target)) throw new MeetingJoinError('return to the authorized Zoom meeting in the browser before returning control');
      } else {
        const joined = page.getByRole('button', { name: /^(Leave call|Leave meeting|Leave)$/i });
        if (await visible(joined)) leave = joined;
        else await joinStep('the meeting page could not be reopened', () => page.goto(target, { waitUntil: 'domcontentloaded', timeout: timeoutMs }));
      }
    }
    const attempt = () => platform === 'meet' ? joinGoogleMeet(page, displayName, timeoutMs, onAdmissionRequested)
      : platform === 'zoom' ? joinZoom(page, displayName, timeoutMs, onAdmissionRequested)
        : joinTeams(page, displayName, timeoutMs, onAdmissionRequested);
    try { if (!leave) leave = await attempt(); }
    catch (error) {
      if (!handoff || signal?.aborted) throw error;
      const reason = await interventionReason(page);
      if (!reason) throw error;
      // The failed join attempt has finished. No automation, audio capture,
      // transcription or presence polling runs while the human signs in.
      await joinStep('browser handoff did not complete', () => handoff.request({ page, context, signal, reason }));
      if (signal?.aborted) throw new MeetingJoinError('the meeting was cancelled');
      if (platform === 'zoom') {
        if (!canResumeZoom(page, target)) throw new MeetingJoinError('return to the authorized Zoom meeting in the browser before returning control');
        leave = await attempt();
      } else {
        // A human may have joined manually. Otherwise return only to the original
        // authorized meeting, never inspect a remaining password or MFA form.
        leave = page.getByRole('button', { name: /^(Leave call|Leave meeting|Leave)$/i });
        if (!await visible(leave)) {
          await joinStep('the meeting page could not be reopened', () => page.goto(target, { waitUntil: 'domcontentloaded', timeout: timeoutMs }));
          leave = await attempt();
        }
      }
    }
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
