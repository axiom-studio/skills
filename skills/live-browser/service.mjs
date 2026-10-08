import {
  BrowserAudio, BrowserDesktopInput, BrowserHandoff, BrowserPausedError, BROWSER_HANDOFF_REASONS,
  CortexConversation, PersistentBrowserProfile, PROFILE_WAIT_MS, createAudioRoute, createBrowserDesktop, detectBrowserIntervention,
  hostInvocation, launchCamoufox, openBrowserRFB, openBrowserVideo,
} from '@axiom/live-browser';
import { matchingLogins, paymentCard, topOrigin, websiteLogins } from './credentials.mjs';
import { LivePage, notActionable } from './page.mjs';
import { pageTotal, SignIn } from './sign-in.mjs';

const ID = /^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$/;
const HUMAN_ONLY = { kind: 'manual_confirmation' };
const MINUTE = 60000;
const SETUP_LOGIN = 'Do not hand off. Ask the user in chat to add a website login for this site with the in-chat setup form ' +
  '(request_setup, kind configure, skill skill-live-browser), with the website set to this origin; continue when it is added. Never ask for the password in chat.';
const SETUP_CARD = 'Do not hand off. Ask the user in chat to add a payment card with the in-chat setup form ' +
  '(request_setup, kind configure, skill skill-live-browser); continue when it is added. Never ask for card details in chat.';

function validID(value, name) {
  if (typeof value !== 'string' || !ID.test(value)) throw new Error(`${name} is invalid`);
  return value;
}

const minutes = ms => Math.min(480, Math.max(1, Math.ceil(ms / MINUTE)));

function intentText(value) {
  if (value === undefined) return undefined;
  if (typeof value !== 'string' || value.length > 500) throw new Error('intent must be at most 500 characters');
  return value.replace(/[\u0000-\u001f\u007f]+/g, ' ').trim().slice(0, 200) || undefined;
}

// The agent's interactive browser. One live Camoufox per registered Cortex
// browser session; at most one per (agent, conversation).
//
// The runtime is per tenant and keeps the tenant's single, real browser
// profile on its persistent volume. Firefox opens a profile only once, and a
// live browser owns its whole display, page audio and take-control desktop,
// so sessions are queued: one live browser at a time, a start from another
// conversation waits (bounded) until it closes. Never log page content,
// inputs, tokens or keys.
export class LiveBrowserService {
  #api; #authorize; #authorizeProfile; #deps; #sessions = new Map(); #options; #profile; #profileRequests = new Map();

  constructor({ api, authorize, authorizeProfile, tenantID, deps = {}, profile = new PersistentBrowserProfile(), humanWaitMs = 120000,
    idleMs = 30 * MINUTE, maxLifetimeMs = 8 * 60 * MINUTE, replyWaitMs = 60 * MINUTE, extendMarginMs = 2 * MINUTE,
    leaseMs = 300000, profileWaitMs = PROFILE_WAIT_MS, closeTimeoutMs = 30000, fetchAPI = fetch, now = Date.now,
    speechBaseURL = 'http://axiomcloud.axiomcd.svc.cluster.local/rest/v1/llm-gateway/v1/' } = {}) {
    if (!api || typeof authorize !== 'function') throw new Error('Live browser host API is required');
    this.#api = api;
    this.#authorize = authorize;
    this.#authorizeProfile = authorizeProfile;
    this.#profile = profile;
    this.#options = { tenantID: tenantID === undefined || tenantID === '' ? undefined : String(tenantID), humanWaitMs, idleMs, maxLifetimeMs, replyWaitMs, extendMarginMs, leaseMs, profileWaitMs, closeTimeoutMs, fetchAPI, now, speechBaseURL };
    this.#deps = { launch: launchCamoufox, createDesktop: createBrowserDesktop, createAudioRoute,
      createAudio: options => new BrowserAudio(options), openVideo: openBrowserVideo, openRFB: openBrowserRFB, desktopInput: options => new BrowserDesktopInput(options), detectIntervention: detectBrowserIntervention,
      ...deps };
  }

  get size() { return this.#sessions.size; }

  #owned(agentID, sessionID) {
    const session = this.#sessions.get(validID(sessionID, 'sessionId'));
    if (!session || session.agentID !== validID(agentID, 'agent ID') || session.closing) {
      throw new Error('No live browser session with this sessionId; call live-browser-start');
    }
    return session;
  }

  async status(session) {
    const handoff = session.handoff;
    // Registered but the display is not up yet: report a starting browser
    // rather than failing, so viewers see the session (and retry the stream).
    const status = handoff ? handoff.status : 'automating';
    let title = session.title;
    if (session.page) {
      try { title = await Promise.race([session.page.title(), new Promise((_, reject) => setTimeout(reject, 1000).unref())]); } catch { /* cached */ }
    }
    session.title = title;
    const lease = handoff?.lease;
    return {
      sessionId: session.id, status, url: session.page?.url() ?? '', title: title ?? '',
      ...(session.step ? { step: session.step } : {}),
      ...(handoff?.intervention ? { intervention: handoff.intervention } : {}),
      ...(lease ? { lease: { expiresAt: new Date(lease.expiresAt).toISOString() } } : {}),
      profile: this.#profile.status,
      audio: { listening: session.audio?.listening === true },
      expiresAt: session.expiresAt,
    };
  }

  async start({ runID, agentID, input, bindings }) {
    agentID = validID(agentID, 'agent ID');
    runID = validID(runID, 'run ID');
    const url = input.url;
    for (const session of this.#sessions.values()) {
      if (session.agentID === agentID && session.runIDs.has(runID) && !session.closing) {
        return this.#act(session, input.intent ?? 'Open page', async live => {
          if (url) await live.navigate(url);
          return {};
        }, { navigation: Boolean(url) });
      }
    }
    const invocation = hostInvocation(bindings.CORTEX_HOST_INVOCATIONS, 'host:browser');
    if (!invocation) throw new Error('This action requires the host:browser:session permission');
    // The session's lifetime is the runtime's: an idle window that slides
    // with use and is extended while the browser waits for the user.
    const registered = await this.#api.register({ invocation, durationMinutes: minutes(this.#options.idleMs) });
    if (registered.agentId !== agentID) {
      await this.#api.revoke({ sessionId: registered.sessionId, grant: registered.grant, tenantId: registered.tenantId }).catch(() => {});
      throw new Error('Browser registration does not match this agent');
    }
    // One browser per (agent, conversation). A later turn of the same
    // conversation (for example after the user replied with a one-time code)
    // reattaches to the open browser and its page instead of replacing it.
    for (const open of this.#sessions.values()) {
      if (open.agentID === agentID && open.conversationID === registered.conversationId && !open.closing) {
        await this.#api.revoke({ sessionId: registered.sessionId, grant: registered.grant, tenantId: registered.tenantId }).catch(() => {});
        open.runIDs.add(runID);
        return this.#act(open, input.intent ?? 'Open page', async live => {
          if (url) await live.navigate(url);
          return {};
        }, { navigation: Boolean(url) });
      }
    }
    // The tenant's profile is open in at most one browser: wait for the
    // current one (another conversation's) to close.
    let release;
    try {
      await this.#profile.load();
      release = await this.#profile.acquire(this.#options.profileWaitMs);
    } catch (error) {
      await this.#api.revoke({ sessionId: registered.sessionId, grant: registered.grant, tenantId: registered.tenantId }).catch(() => {});
      throw new Error(error?.expose === true ? error.message : 'The live browser could not start');
    }
    const created = this.#options.now();
    const session = { id: registered.sessionId, grant: registered.grant, tenantID: registered.tenantId, agentID,
      conversationID: registered.conversationId, expiresAt: registered.expiresAt, runIDs: new Set([runID]),
      createdAt: created, activeAt: created, deadline: created + this.#options.maxLifetimeMs,
      controller: new AbortController(), requests: new Map(), video: new Set(), release };
    this.#sessions.set(session.id, session);
    try {
      session.desktop = await this.#deps.createDesktop({ signal: session.controller.signal });
      const display = session.desktop.display;
      // The handoff (status, live view, control) exists as soon as the display
      // does. Launching Camoufox and loading the profile take seconds to tens
      // of seconds; viewers watch the browser come up instead of waiting.
      session.handoff = new BrowserHandoff({ tenantID: session.tenantID, agentID, leaseMs: this.#options.leaseMs,
        close: () => this.#teardown(session),
        onState: (status, intervention) => {
          // Waiting for the user keeps the browser (and its Cortex session) open.
          session.activeAt = this.#options.now();
          if (status === 'awaiting_user') {
            this.#handoffNotice(session, intervention);
            void this.#extend(session).then(() => this.#arm(session), () => {});
          } else this.#arm(session);
        },
        videoFactory: ({ signal }) => this.#deps.openVideo({ display, signal }),
        desktopFactory: ({ signal }) => this.#deps.openRFB({ display, signal }),
        inputFactory: ({ control }) => this.#deps.desktopInput({ display, control, signal: session.controller.signal }),
      });
      session.route = await this.#deps.createAudioRoute(session.id.replace(/[^a-z0-9]/gi, '').toLowerCase().slice(0, 24));
      // The persistent profile: everything from earlier sessions is there
      // before the first page loads.
      session.launching = this.#deps.launch(this.#profile.root, { display: session.desktop.display,
        audio: { sink: session.route.sink, source: session.route.source } });
      session.context = await session.launching;
      if (session.closed) throw new Error('closed while starting');
      this.#profile.track(session.context);
      session.page = session.context.pages()[0] ?? await session.context.newPage();
      session.live = new LivePage(session.page);
      session.page.on?.('close', () => { void this.close(session); });
      this.#arm(session);
    } catch {
      await this.close(session);
      throw new Error('The live browser could not start');
    }
    return this.#act(session, input.intent ?? 'Open page', async live => {
      if (url) await live.navigate(url);
      return {};
    }, { navigation: Boolean(url) });
  }

  // Runs one model action. Waits while a human holds control and then
  // returns paused_by_user instead of acting.
  async #act(session, intent, operation, { navigation = false } = {}) {
    const step = intentText(intent);
    this.#touch(session);
    if (session.handoff.status === 'human') return this.#paused(session);
    if (session.handoff.status === 'awaiting_user') return this.#awaiting(session);
    let result;
    try {
      session.busy = (session.busy ?? 0) + 1;
      result = await session.handoff.automate(async () => {
        if (step) session.step = step;
        return operation(session.live);
      });
    } catch (error) {
      if (error instanceof BrowserPausedError) return error.status === 'human' ? this.#paused(session) : this.#awaiting(session);
      if (error?.notActionable === true) return this.#notActionable(session, error);
      throw new Error(error?.expose === true ? error.message : 'The browser action failed');
    } finally {
      session.busy--;
      this.#touch(session);
    }
    const { checkChallenges, handoffReason, ...output } = result ?? {};
    // CAPTCHAs and bot checks need the human: hand off. A one-time code page
    // is not handed off here; the agent signs in (live-browser-sign-in) or
    // hands off as its instructions say.
    let reason = handoffReason;
    if (!reason && (navigation || checkChallenges)) {
      const found = await this.#deps.detectIntervention(session.page).catch(() => undefined);
      reason = found === 'challenge' ? 'captcha' : undefined;
    }
    if (reason && session.handoff.status === 'automating') return { ...output, ...(await this.#handoff(session, reason, undefined, true)) };
    return { sessionId: session.id, status: session.handoff.status, url: session.page.url(),
      title: await session.page.title().catch(() => ''), ...output };
  }

  // An element the model targeted could not be acted on. Nothing was changed
  // in a way that needs undoing, so this is a successful result the model can
  // recover from, not an action failure (which ends a write action's run).
  async #notActionable(session, error) {
    return { sessionId: session.id, status: 'not_actionable', browserStatus: session.handoff.status,
      reason: String(error.reason ?? error.message).slice(0, 200), hint: String(error.hint ?? 'Take a new snapshot and try another way.').slice(0, 300),
      url: session.page.url(), title: await session.page.title().catch(() => ''), requiresHuman: false };
  }

  async #paused(session) {
    const lease = session.handoff.lease;
    const wait = Math.max(0, Math.min(this.#options.humanWaitMs, lease ? lease.expiresAt - this.#options.now() : this.#options.humanWaitMs));
    const state = await session.handoff.settled(wait);
    const resumed = state === 'automating';
    return { sessionId: session.id, status: 'paused_by_user', browserStatus: state, url: session.page.url(),
      requiresHuman: !resumed, ...(resumed ? {} : { challenges: [HUMAN_ONLY.kind], challenge: HUMAN_ONLY }),
      message: resumed
        ? 'The user took control and has handed back. The page may have changed: take a new snapshot before continuing.'
        : 'The user is controlling the browser. Wait for them to hand back control; do not act on this page.' };
  }

  #awaiting(session) {
    return { sessionId: session.id, status: 'awaiting_user', intervention: session.handoff.intervention, url: session.page.url(),
      requiresHuman: false, message: 'Waiting for the user to take control and hand back. Do not act on this page until they do.' };
  }

  // Best effort: Cortex posts "I need you to take over" to the chat and linked
  // threads. Never blocks the handoff; failures are logged without content.
  #handoffNotice(session, intervention) {
    session.handoffs = (session.handoffs ?? 0) + 1;
    const handoffId = `${session.id}:h${session.handoffs}`.replace(/[^A-Za-z0-9_:-]/g, '').slice(0, 128);
    void Promise.resolve().then(() => this.#api.handoffNotice({ sessionId: session.id, grant: session.grant, tenantId: session.tenantID },
      { handoffId, summary: intervention?.summary ?? '' })).catch(error => {
      console.warn(JSON.stringify({ event: 'live_browser_handoff_notice_failed', status: error?.status ?? null }));
    });
  }

  async #handoff(session, reason, summary, automatic = false) {
    const intervention = await session.handoff.requestHandoff(reason, summary);
    return { sessionId: session.id, status: 'awaiting_user', intervention, url: session.page.url(),
      requiresHuman: true, challenges: [HUMAN_ONLY.kind], challenge: HUMAN_ONLY,
      message: automatic ? 'The page needs a human (sign-in code or verification). The user was asked to take control.'
        : 'The user was asked to take control. Continue after they hand back.' };
  }

  async execute(action, { runID, agentID, input, bindings }) {
    if (action === 'live-browser-start') return this.start({ runID, agentID, input, bindings });
    const session = this.#owned(agentID, input.sessionId);
    session.runIDs.add(validID(runID, 'run ID'));
    switch (action) {
      case 'live-browser-navigate':
        return this.#act(session, input.intent ?? 'Navigate', async live => live.navigate(input.url), { navigation: true });
      case 'live-browser-snapshot':
        return this.#act(session, input.intent, async live => {
          const snapshot = await live.snapshot({ includeScreenshot: input.includeScreenshot === true });
          const handoffReason = snapshot.challenges.some(kind => kind !== 'mfa') ? 'captcha' : undefined;
          // Never show the model a screenshot of card details it filled.
          const withheld = snapshot.modelMedia && this.#redacted(session);
          if (withheld) delete snapshot.modelMedia;
          return { ...snapshot, requiresHuman: false, ...(withheld ? { screenshotWithheld: true } : {}), ...(handoffReason ? { handoffReason } : {}) };
        });
      case 'live-browser-click':
        return this.#act(session, input.intent, async live => { await live.click(input); return { done: true, checkChallenges: true }; });
      case 'live-browser-fill':
        return this.#act(session, input.intent, async live => ({ ...(await live.fill(input)), done: true }));
      case 'live-browser-select':
        return this.#act(session, input.intent, async live => ({ ...(await live.select(input)), done: true, checkChallenges: true }));
      case 'live-browser-scroll':
        return this.#act(session, input.intent ?? 'Scroll', async live => { await live.scroll(input); return { done: true }; });
      case 'live-browser-screenshot':
        return this.#act(session, input.intent, async live => {
          if (this.#redacted(session)) throw notActionable('Screenshots are off while card details are on this page', 'Use live-browser-snapshot instead.');
          return { modelMedia: await live.screenshot(input) };
        });
      case 'live-browser-sign-in':
        return this.#signIn(session, input, bindings);
      case 'live-browser-fill-payment-card':
        return this.#fillCard(session, input, bindings);
      case 'live-browser-request-handoff': {
        if (!BROWSER_HANDOFF_REASONS.includes(input.reason)) throw new Error('Unsupported handoff reason');
        if (session.handoff.status === 'human') return this.#paused(session);
        if (session.handoff.status === 'awaiting_user') return { ...this.#awaiting(session), requiresHuman: true, challenges: [HUMAN_ONLY.kind], challenge: HUMAN_ONLY };
        return this.#handoff(session, input.reason, input.summary);
      }
      case 'live-browser-close':
        if (session.handoff.status === 'human') return this.#paused(session);
        await this.close(session);
        return { sessionId: session.id, status: 'none' };
      case 'live-browser-listen':
        return this.#listen(session, input);
      case 'live-browser-speak':
        if (session.handoff.status === 'human') return this.#paused(session);
        return { sessionId: session.id, ...(await this.#audio(session).speak(input.text, { speechModel: input.speechModel, voice: input.voice })) };
      default: throw new Error('Unknown live browser action');
    }
  }

  // Lifetime: an idle window that slides with every action. While the
  // browser waits for the user (handoff, human control, an in-flight action
  // or a one-time code asked for in chat) it is extended instead of closed.
  // The Cortex session is extended ahead of its expiry; 8 hours is the cap.
  #touch(session) {
    if (session.closed) return;
    session.activeAt = this.#options.now();
    this.#arm(session);
  }

  #waiting(session) {
    const status = session.handoff?.status;
    return status === 'awaiting_user' || status === 'human' || (session.busy ?? 0) > 0 || (session.replyUntil ?? 0) > this.#options.now();
  }

  #arm(session) {
    if (session.closed || !session.deadline) return;
    clearTimeout(session.expiry);
    const now = this.#options.now();
    const expires = Date.parse(session.expiresAt);
    const refresh = expires - this.#options.extendMarginMs > now ? expires - this.#options.extendMarginMs : expires;
    const at = Math.min(session.activeAt + this.#options.idleMs, refresh, session.deadline);
    // A failed extend is retried later, but never past the session's expiry.
    const delay = Math.min(Math.max(session.retryMs ?? 20, at - now), Math.max(20, expires - now));
    session.expiry = setTimeout(() => { void this.#expire(session); }, delay);
    session.expiry.unref?.();
  }

  async #expire(session) {
    if (session.closed) return;
    const now = this.#options.now();
    if (now >= session.deadline || !(now < Date.parse(session.expiresAt))) return this.close(session);
    if (this.#waiting(session)) session.activeAt = now;
    else if (now >= session.activeAt + this.#options.idleMs) return this.close(session);
    try {
      await this.#extend(session);
      session.retryMs = undefined;
    } catch {
      session.retryMs = 30000;
    }
    this.#arm(session);
  }

  async #extend(session) {
    const remaining = session.deadline - this.#options.now();
    if (session.closed || remaining < MINUTE) return;
    const extended = await this.#api.extend({ sessionId: session.id, grant: session.grant, tenantId: session.tenantID },
      { durationMinutes: Math.min(minutes(this.#options.idleMs), Math.floor(remaining / MINUTE)) });
    if (session.closed) return;
    session.expiresAt = extended.expiresAt;
    if (extended.grant) session.grant = extended.grant;
  }

  #redacted(session) {
    return Boolean(session.redactURL) && session.page?.url() === session.redactURL;
  }

  // Signs in with a saved website login bound to this action whose website
  // is exactly the page's top-level origin. Values come only from bindings.
  async #signIn(session, input, bindings) {
    if (input.credential !== undefined && /[\u0000-\u001f\u007f]/.test(input.credential)) throw new Error('credential is invalid');
    const logins = websiteLogins(bindings);
    const code = input.oneTimeCode === undefined ? undefined : input.oneTimeCode.replace(/[\s-]/g, '');
    session.replyUntil = undefined;
    return this.#act(session, input.intent ?? 'Sign in', async live => {
      const origin = topOrigin(live.page.url());
      const base = { origin: origin ?? '', requiresHuman: false };
      const candidates = matchingLogins(logins, origin, input.credential);
      if (!candidates.length) {
        const named = input.credential !== undefined && matchingLogins(logins, origin).length > 0;
        return { ...base, status: 'no_matching_login', message: named
          ? `The login "${input.credential}" is not saved for ${origin}. Call again without credential to use the login saved for this site.`
          : `No saved login for ${origin || 'this page'}. ${SETUP_LOGIN}` };
      }
      if (candidates.length > 1) {
        return { ...base, status: 'choose_login', logins: candidates.map(login => login.label),
          message: 'Several saved logins are for this site. Call live-browser-sign-in again with credential set to one of logins; ask the user in chat which one if unclear.' };
      }
      const login = candidates[0];
      const outcome = await new SignIn(live).signIn(login, { oneTimeCode: code, now: this.#options.now });
      const result = { ...base, credential: login.label, ...(outcome.step ? { step: outcome.step } : {}) };
      switch (outcome.state) {
        case 'otp_required':
          session.replyUntil = this.#options.now() + this.#options.replyWaitMs;
          return { ...result, status: 'otp_required', ...(outcome.retry ? { retry: true } : {}), message: (outcome.retry
            ? 'The site did not accept the code (wrong or expired). Ask the user in chat for the new code, then call live-browser-sign-in again with oneTimeCode.'
            : 'The site sent a one-time code (SMS or email). Do not hand off. Ask the user in chat: "Might have gotten an OTP, please provide", wait for their reply, then call live-browser-sign-in with oneTimeCode set to their code.') +
            ` Keep using sessionId ${session.id}; the browser stays open on this page while you wait.` };
        case 'origin_changed':
          return { ...base, origin: topOrigin(live.page.url()) ?? '', status: 'no_matching_login', step: outcome.step,
            message: `Sign-in moved to ${topOrigin(live.page.url()) ?? 'another site'}, which no saved login is for; nothing was typed there. ${SETUP_LOGIN}` };
        case 'no_code_field':
          throw notActionable('No one-time code field on this page', 'Take a new snapshot. If the site asks for the code elsewhere, open that step first.');
        case 'no_form':
          throw notActionable('No sign-in form on this page', "Click the site's Sign in link, take a new snapshot, then call live-browser-sign-in again.");
        default:
          return { ...result, status: 'submitted', message: outcome.stillOnForm
            ? 'The sign-in form is still shown, so the saved login may be wrong. Take a snapshot; if the site says the login is wrong, tell the user in chat and ask them to update the login with the in-chat setup form.'
            : 'The saved login was submitted. Take a new snapshot to confirm you are signed in.' };
      }
    });
  }

  // Fills the saved payment card into the checkout's card fields (also in
  // the page's payment processor frames). It never submits.
  async #fillCard(session, input, bindings) {
    if (!(Number(input.amount) > 0)) throw new Error('amount must be greater than zero');
    const card = paymentCard(bindings);
    return this.#act(session, input.intent ?? 'Fill the payment card', async live => {
      const base = { amount: input.amount, currency: input.currency, requiresHuman: false };
      if (!card) return { ...base, status: 'no_payment_card', message: `No saved payment card. ${SETUP_CARD}` };
      if (card.refused) {
        return { ...base, status: 'spend_cap_exceeded', ...(card.remaining ? { remaining: card.remaining } : {}), ...(card.cap ? { cap: card.cap } : {}),
          ...(card.currency ? { capCurrency: card.currency } : {}), message: 'This charge is over the saved card\'s remaining spend cap (or in another currency). Nothing was filled. ' +
          'Do not hand off and do not try another way: ask the user in chat to raise the cap with the in-chat setup form (request_setup, kind configure, skill skill-live-browser), then call again.' };
      }
      const text = await live.page.evaluate('document.body ? document.body.innerText.slice(0, 200000) : ""').catch(() => '');
      const total = pageTotal(text);
      if (total && (!total.currency || total.currency === input.currency) && total.amount > Number(input.amount) + 0.005) {
        return { ...base, status: 'amount_mismatch', pageTotal: String(total.amount), ...(total.currency ? { pageCurrency: total.currency } : {}),
          message: 'The order total on the page is higher than the amount you declared. Nothing was filled. Read the final total and call again with that exact amount and currency.' };
      }
      const { filled } = await new SignIn(live).fillCard(card);
      if (!filled.includes('number')) {
        throw notActionable('No card number field on this page', "Choose the checkout's card payment option (for example 'Credit or debit card' or 'Add a new card'), take a new snapshot, then call again.");
      }
      session.redactURL = live.page.url();
      return { ...base, status: 'card_filled', filled, message: 'The saved card was filled. Take a snapshot to check the order and any errors, then complete the payment.' };
    });
  }

  // Page audio uses the session grant: Cortex accepts it only when the
  // session's registration carried host:browser:audio.
  #audio(session) {
    if (session.audio) return session.audio;
    const grantSession = { sessionId: session.id, grant: session.grant, tenantId: session.tenantID };
    const conversation = new CortexConversation({ baseURL: this.#api.sessionURL(session.id), grant: session.grant,
      tenantID: session.tenantID, agentID: session.agentID, conversationID: session.conversationID, sessionID: session.id,
      fetchAPI: this.#options.fetchAPI });
    session.audio = this.#deps.createAudio({ route: session.route, conversation, agentID: session.agentID,
      agentLabel: session.agentLabel ?? 'Agent', speechBaseURL: this.#options.speechBaseURL, fetchAPI: this.#options.fetchAPI,
      grants: { catalog: () => this.#api.audioCatalogGrant(grantSession),
        audio: selection => this.#api.audioGrants(grantSession, selection) } });
    return session.audio;
  }

  async #listen(session, input) {
    if (input.state === 'off') {
      session.audio?.stopListening();
      return { sessionId: session.id, listening: false };
    }
    if (input.displayName !== undefined) session.agentLabel = input.displayName;
    const audio = this.#audio(session);
    if (input.displayName !== undefined) audio.setAgentLabel(input.displayName);
    const state = await audio.listen({ speakerLabel: input.speakerLabel,
      wakePhrases: [...(input.displayName ? [input.displayName] : []), ...(input.wakePhrases ?? [])],
      speakReplies: input.speakReplies ?? true, transcriptionModel: input.transcriptionModel, speechModel: input.speechModel, voice: input.voice });
    return { sessionId: session.id, ...state };
  }

  // Host-only: verifies a human proof for this exact command with Cortex.
  async #authorized({ agentID, sessionID, authorization, command }) {
    if (!command || typeof command !== 'object' || Array.isArray(command) || Buffer.byteLength(JSON.stringify(command)) > 32768) throw new Error();
    const session = this.#sessions.get(validID(sessionID, 'session ID'));
    if (!session || session.agentID !== validID(agentID, 'agent ID') || session.closing) throw new Error();
    const principal = await this.#authorize({ authorization, command, tenantID: session.tenantID, agentID,
      sessionID: session.id, conversationID: session.conversationID, sessionGrant: session.grant });
    if (typeof principal?.userID !== 'string' || !principal.userID.trim() || principal.tenantID !== session.tenantID ||
      principal.agentID !== agentID || typeof principal.requestID !== 'string' || !ID.test(principal.requestID) ||
      !(Date.parse(principal.expiresAt) > this.#options.now())) throw new Error();
    for (const [id, expiry] of session.requests) if (expiry <= this.#options.now()) session.requests.delete(id);
    if (session.requests.has(principal.requestID) || session.requests.size >= 256) throw new Error();
    session.requests.set(principal.requestID, Date.parse(principal.expiresAt));
    if (session.closing) throw new Error();
    return { session, principal: { tenantID: principal.tenantID, agentID: principal.agentID, userID: principal.userID, expiresAt: principal.expiresAt } };
  }

  async controlBrowser(request) {
    try {
      const { session, principal } = await this.#authorized(request);
      const { command } = request;
      if (command.type === 'status') {
        if (Object.keys(command).length !== 1) throw new Error();
        return this.status(session);
      }
      return await session.handoff.handle(principal, command);
    } catch { throw new Error('Browser control request could not be completed'); }
  }

  // Host-only profile commands: the conversation's "saved sign-ins" view and
  // "Forget sign-ins". They need no open browser; the proof is verified by
  // Cortex and must be for the tenant this runtime serves.
  async controlProfile({ agentID, authorization, command }) {
    try {
      if (!command || typeof command !== 'object' || Array.isArray(command) || Object.keys(command).length !== 1 ||
        !['profileStatus', 'forgetProfile'].includes(command.type) || !this.#authorizeProfile || !this.#options.tenantID) throw new Error();
      const principal = await this.#authorizeProfile({ authorization, command, tenantID: this.#options.tenantID });
      if (principal?.tenantID !== this.#options.tenantID || principal.agentID !== validID(agentID, 'agent ID') ||
        typeof principal.userID !== 'string' || !principal.userID.trim() || typeof principal.requestID !== 'string' ||
        !ID.test(principal.requestID) || !(Date.parse(principal.expiresAt) > this.#options.now())) throw new Error();
      for (const [id, expiry] of this.#profileRequests) if (expiry <= this.#options.now()) this.#profileRequests.delete(id);
      if (this.#profileRequests.has(principal.requestID) || this.#profileRequests.size >= 256) throw new Error();
      this.#profileRequests.set(principal.requestID, Date.parse(principal.expiresAt));
      if (command.type === 'profileStatus') return await this.#profile.details();
      return { deleted: await this.forgetProfile() };
    } catch { throw new Error('Browser profile request could not be completed'); }
  }

  // Stops every live browser cleanly (Firefox flushes and exits), then
  // deletes the profile before any queued browser may start.
  async forgetProfile() {
    const lease = this.#profile.acquire(this.#options.profileWaitMs, { first: true });
    await Promise.all([...this.#sessions.values()].map(session => this.close(session)));
    const release = await lease;
    try {
      const existed = (await this.#profile.details()).state !== 'none';
      await this.#profile.forget();
      console.warn(JSON.stringify({ event: 'live_browser_profile_forgotten' }));
      return existed;
    } finally { release(); }
  }

  async videoBrowser(request, desktop = false) {
    try {
      const { command } = request;
      const watch = !desktop && command?.type === 'watch' && Object.keys(command).length === 1;
      if (!watch && (command?.type !== (desktop ? 'desktop' : 'video') || typeof command.leaseID !== 'string' ||
        !ID.test(command.leaseID) || Object.keys(command).some(key => !['type', 'leaseID'].includes(key)))) throw new Error();
      const { session, principal } = await this.#authorized(request);
      const controller = new AbortController();
      let timer;
      const expire = expiresAt => {
        clearTimeout(timer);
        const remaining = Date.parse(expiresAt) - this.#options.now();
        if (!(remaining > 0) || remaining > 16000) throw new Error();
        timer = setTimeout(() => controller.abort(), remaining);
        timer.unref?.();
      };
      expire(principal.expiresAt);
      const stream = watch ? session.handoff.watch({ signal: controller.signal })
        : session.handoff.stream(principal, command.leaseID, { signal: controller.signal, desktop });
      const entry = { abort: () => controller.abort() };
      session.video.add(entry);
      return {
        stream,
        write: bytes => desktop ? session.handoff.writeDesktop(principal, command.leaseID, bytes) : Promise.reject(new Error()),
        renew: async renewal => {
          if (renewal.agentID !== request.agentID || renewal.sessionID !== request.sessionID ||
            JSON.stringify(renewal.command) !== JSON.stringify(command)) throw new Error('Browser video authorization failed');
          const verified = await this.#authorized(renewal);
          if (verified.session !== session || verified.principal.userID !== principal.userID) throw new Error('Browser video authorization failed');
          expire(verified.principal.expiresAt);
        },
        close: () => { clearTimeout(timer); controller.abort(); session.video.delete(entry); },
      };
    } catch { throw new Error('Browser video is unavailable'); }
  }

  // Ends a browser: closes Camoufox (which flushes the profile to the
  // volume), releases the profile and revokes the Cortex session (which ends
  // the user's live view).
  async close(session) {
    if (!session) return;
    if (session.handoff) await session.handoff.close();
    else await this.#teardown(session);
  }

  async #teardown(session) {
    if (session.closed) return;
    session.closed = session.closing = true;
    clearTimeout(session.expiry);
    for (const video of session.video) video.abort();
    session.audio?.close();
    // The profile stays leased until Firefox has really exited, even when
    // closing takes longer than the bounded wait here.
    // A browser still launching is closed once it is up.
    const context = session.context ?? await session.launching?.catch(() => undefined);
    let exited = Promise.resolve();
    if (context) {
      // A clean Firefox shutdown leaves consistent SQLite files.
      let timer;
      exited = context.close().catch(() => {});
      const closed = await Promise.race([exited.then(() => true),
        new Promise(resolve => { timer = setTimeout(() => resolve(false), this.#options.closeTimeoutMs); timer.unref?.(); })]);
      clearTimeout(timer);
      if (!closed) console.warn(JSON.stringify({ event: 'live_browser_close_timeout' }));
    }
    await this.#profile.flush().catch(() => console.warn(JSON.stringify({ event: 'live_browser_sites_write_failed' })));
    session.controller.abort();
    session.desktop?.close();
    await session.route?.close().catch(() => {});
    this.#sessions.delete(session.id);
    void exited.finally(() => session.release?.());
    await this.#api.revoke({ sessionId: session.id, grant: session.grant, tenantId: session.tenantID }).catch(() => {});
  }

  async closeAll() {
    this.#profile.cancelWaiters();
    await Promise.all([...this.#sessions.values()].map(session => this.close(session)));
  }

  ready() { return true; }
}
