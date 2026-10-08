// Cortex browser-session API client (CORTEX_BROWSER_API_URL, e.g.
// http://sentinel/orchestrator/agent/browser/v1/). Tokens are sent only as
// headers or bodies to that base URL and never appear in errors or logs.
const ID = /^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$/;

// Selects one host-service invocation grant from the CORTEX_HOST_INVOCATIONS
// binding, a JSON map keyed by audience (for example "host:browser").
export function hostInvocation(binding, audience = 'host:browser') {
  if (binding === undefined || binding === null || binding === '') return undefined;
  try {
    const grants = typeof binding === 'string' ? JSON.parse(binding) : binding;
    if (!grants || typeof grants !== 'object' || Array.isArray(grants)) throw new Error();
    const token = grants[audience];
    if (token === undefined) return undefined;
    if (typeof token !== 'string' || !token || token.length > 16384) throw new Error();
    return token;
  } catch {
    // Parser diagnostics may contain the secret input. Never return them.
    throw new Error('Invalid host invocation binding');
  }
}

function text(value, name, pattern) {
  if (typeof value !== 'string' || !value.trim() || value.length > 16384 || (pattern && !pattern.test(value))) {
    throw new Error(`Cortex returned an invalid ${name}`);
  }
  return value;
}

function future(value, name) {
  const at = Date.parse(value);
  if (!Number.isFinite(at) || at <= Date.now()) throw new Error(`Cortex returned an invalid ${name}`);
  return new Date(at).toISOString();
}

// Typed Cortex refusals the agent must explain to the user. Only the code is
// read from the reply; the message is owned here, so server text is never
// relayed.
const TYPED_REFUSALS = Object.freeze({
  browser_no_conversation: 'This task has no conversation to show the live browser in. '
    + 'The live browser only runs for a chat or a task that posts to a conversation; tell the user, and use a non-interactive browser if one is available.',
});

async function typedRefusal(response) {
  if (response.status !== 409) return undefined;
  try {
    const code = (await response.json())?.errorCode;
    return typeof code === 'string' && Object.hasOwn(TYPED_REFUSALS, code) ? code : undefined;
  } catch { return undefined; }
}

export class BrowserSessionAPI {
  #base; #fetch;

  constructor({ baseURL, fetchAPI = fetch }) {
    if (typeof baseURL !== 'string' || !baseURL) throw new Error('Cortex browser API URL is required');
    const base = new URL(baseURL.endsWith('/') ? baseURL : `${baseURL}/`);
    if (!['http:', 'https:'].includes(base.protocol) || base.username || base.password) throw new Error('Cortex browser API URL is invalid');
    this.#base = base;
    this.#fetch = fetchAPI;
  }

  url(path) { return new URL(path, this.#base); }

  // Raw fetch for presigned object-storage URLs issued by Cortex.
  get fetchAPI() { return this.#fetch; }

  // Base for the session's conversation, messages and transcript routes,
  // authenticated with the session grant (requires host:browser:audio).
  sessionURL(sessionID) { return this.url(`sessions/${encodeURIComponent(text(sessionID, 'session', ID))}/`).toString(); }

  async request(path, { invocation, grant, tenantID, body, method = 'POST', headers: extra = {} }) {
    const headers = { ...(method === 'POST' ? { 'Content-Type': 'application/json' } : {}), ...extra };
    // Host invocation must be the only identity on registration routes.
    if (invocation) headers['X-Cortex-Host-Invocation'] = invocation;
    else {
      headers.Authorization = `Bearer ${grant}`;
      if (tenantID) headers['X-Tenant-ID'] = String(tenantID);
    }
    let response;
    try {
      response = await this.#fetch(this.url(path), { method, headers, redirect: 'error',
        signal: AbortSignal.timeout(30000), ...(method === 'POST' ? { body: JSON.stringify(body ?? {}) } : {}) });
    } catch { throw new Error('Cortex browser API is unavailable'); }
    if (!response.ok) {
      const refusal = await typedRefusal(response);
      if (refusal) throw Object.assign(new Error(TYPED_REFUSALS[refusal]), { status: response.status, code: refusal, expose: true });
      throw Object.assign(new Error(`Cortex browser API refused the request (HTTP ${response.status})`), { status: response.status });
    }
    let envelope;
    try { envelope = await response.json(); } catch { throw new Error('Cortex returned no result'); }
    if (!envelope?.result || typeof envelope.result !== 'object') throw new Error('Cortex returned no result');
    return envelope.result;
  }

  // POST sessions (host:browser:session). One browser session per call.
  async register({ invocation, durationMinutes }) {
    text(invocation, 'host invocation');
    if (!Number.isInteger(durationMinutes) || durationMinutes < 1 || durationMinutes > 480) throw new Error('Invalid browser duration');
    const result = await this.request('sessions', { invocation, body: { durationMinutes } });
    return {
      sessionId: text(result.sessionId, 'browser session', ID),
      grant: text(result.grant, 'browser grant'),
      expiresAt: future(result.expiresAt, 'browser expiry'),
      tenantId: text(String(result.tenantId ?? ''), 'tenant', ID),
      agentId: text(String(result.agentId ?? ''), 'agent', ID),
      conversationId: text(result.conversationId, 'conversation', ID),
    };
  }

  // POST sessions/{id}/revoke with the runtime grant.
  async revoke({ sessionId, grant, tenantId }) {
    await this.request(`sessions/${encodeURIComponent(sessionId)}/revoke`, { grant, tenantID: tenantId });
  }

  #session(path, session, options = {}) {
    return this.request(`sessions/${encodeURIComponent(session.sessionId)}/${path}`,
      { grant: session.grant, tenantID: session.tenantId, ...options });
  }

  // POST sessions/{id}/handoff-notice: Cortex posts the take-over link in chat.
  async handoffNotice(session, { handoffId, summary }) {
    return this.#session('handoff-notice', session, { body: { handoffId, summary } });
  }

  // POST sessions/{id}/audio/catalog-grant -> {token, expiresAt} for the gateway model catalog.
  async audioCatalogGrant(session) {
    const result = await this.#session('audio/catalog-grant', session, { body: {} });
    return { token: text(result.token, 'catalog grant'), expiresAt: future(result.expiresAt, 'catalog grant expiry') };
  }

  // POST sessions/{id}/audio/grants {transcriptionModel, speechModel}.
  async audioGrants(session, { transcriptionModel, speechModel }) {
    const result = await this.#session('audio/grants', session, { body: { transcriptionModel, speechModel } });
    return { transcriptionToken: text(result.transcriptionToken, 'transcription grant'),
      speechToken: text(result.speechToken, 'speech grant'), expiresAt: future(result.expiresAt, 'audio grant expiry') };
  }
}
