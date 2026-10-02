import { chromium } from 'playwright-core';

const MEET_HOST = 'meet.google.com';

export function meetingURL(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new Error('a Google Meet URL is required');
  }
  if (url.protocol !== 'https:' || url.hostname !== MEET_HOST || url.port || url.username || url.password ||
      url.search || url.hash || !/^\/[a-z]{3}-[a-z]{4}-[a-z]{3}\/?$/.test(url.pathname)) {
    throw new Error('a direct https://meet.google.com meeting link is required');
  }
  return url.toString();
}

export async function joinMeet({ url, profileDir, executablePath, displayName = 'Axiom Agent', timeoutMs = 120000, chromiumAPI = chromium, signal, onAdmissionRequested }) {
  if (!profileDir) throw new Error('a browser profile directory is required');
  const target = meetingURL(url);
  const context = await chromiumAPI.launchPersistentContext(profileDir, {
    executablePath,
    headless: true,
    permissions: ['microphone'],
    args: [
      '--no-sandbox',
      '--disable-dev-shm-usage',
      '--use-fake-ui-for-media-stream',
      '--autoplay-policy=no-user-gesture-required',
    ],
  });
  const abort = () => { void context.close().catch(() => {}); };
  if (signal?.aborted) abort();
  else signal?.addEventListener('abort', abort, { once: true });
  try {
    const page = context.pages()[0] ?? await context.newPage();
    await page.goto(target, { waitUntil: 'domcontentloaded', timeout: timeoutMs });
    // Google permits guest entry when the host admits it. A signed-in profile
    // has no name field and continues through the same join path.
    const guestName = page.getByRole('textbox', { name: /^(Your name|Name)$/i });
    if (await guestName.isVisible().catch(() => false)) await guestName.fill(displayName);
    const muted = page.getByRole('button', { name: /^Turn on microphone$/i });
    if (await muted.isVisible().catch(() => false)) await muted.click();
    const join = page.getByRole('button', { name: /^(Join now|Ask to join)$/i });
    await join.waitFor({ timeout: timeoutMs });
    const admissionRequired = await page.getByRole('button', { name: /^Ask to join$/i }).isVisible().catch(() => false);
    await join.click();
    if (admissionRequired) onAdmissionRequested?.();
    // The admission screen can persist until the host allows this participant.
    await page.getByRole('button', { name: /^(Leave call|Leave meeting)$/i })
      .waitFor({ timeout: timeoutMs });
    return {
      context,
      page,
      async leave() {
        try {
          await page.getByRole('button', { name: /^(Leave call|Leave meeting)$/i }).click({ timeout: 5000 });
        } catch {
          // The call can end remotely before teardown reaches this button.
        } finally {
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
