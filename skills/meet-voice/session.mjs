import { fork } from 'node:child_process';
import { randomUUID } from 'node:crypto';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { meetingURL } from './meet.mjs';
import { availableSpeechModels } from './bridge.mjs';

const ID = /^[A-Za-z0-9][A-Za-z0-9_:-]{0,127}$/;

function required(value, name) {
  const text = String(value ?? '').trim();
  if (!text) throw new Error(`${name} is required`);
  return text;
}

function secret(value, name) {
  if (typeof value !== 'string' || !value.trim()) throw new Error(`${name} is required`);
  return value.trim();
}

function selection(bound, fallback, name, maximum) {
  const value = bound ?? fallback;
  if (typeof value !== 'string' || !value.trim() || value.length > maximum) {
    throw new Error(`${name} is required (use meet-models)`);
  }
  return value.trim();
}

function validID(value, name) {
  const text = required(value, name);
  if (!ID.test(text)) throw new Error(`${name} is invalid`);
  return text;
}

// A Skill action's Run is the authority for the destination Seal Chat. A
// model-supplied conversation ID is never accepted by this service.
export class MeetSessionService {
  constructor({ baseURL, tenantID, profilesDir, fetchAPI = fetch, forkProcess = fork, speechConfig = process.env,
    terminationGraceMs = 10000 }) {
    this.baseURL = required(baseURL, 'Cortex Meet API URL');
    this.tenantID = required(tenantID, 'tenant ID');
    this.profilesDir = required(profilesDir, 'profiles directory');
    this.fetchAPI = fetchAPI;
    this.forkProcess = forkProcess;
    this.speechConfig = speechConfig;
    this.terminationGraceMs = terminationGraceMs;
    this.sessions = new Map();
    this.statePath = join(this.profilesDir, '.meet-voice-state.json');
    this.restorePromise = null;
    this.pendingWrite = Promise.resolve();
  }

  async restore() {
    if (!this.restorePromise) this.restorePromise = (async () => {
      let snapshot;
      try {
        snapshot = JSON.parse(await readFile(this.statePath, 'utf8'));
      } catch (error) {
        if (error.code === 'ENOENT') return;
        throw new Error('Meet session state could not be read');
      }
      if (snapshot.version !== 1 || snapshot.tenantID !== this.tenantID || !Array.isArray(snapshot.sessions)) {
        throw new Error('Meet session state does not match this tenant');
      }
      for (const value of snapshot.sessions) {
        if (!value || !ID.test(value.agentID) || !ID.test(value.conversationID) ||
            typeof value.id !== 'string' || typeof value.meetURL !== 'string' ||
            typeof value.startedAt !== 'string' || typeof value.expiresAt !== 'string') {
          throw new Error('Meet session state is invalid');
        }
        const interrupted = !['ended', 'failed'].includes(value.status);
        this.sessions.set(value.agentID, {
          id: value.id, agentID: value.agentID, conversationID: value.conversationID,
          meetURL: value.meetURL, startedAt: value.startedAt, expiresAt: value.expiresAt,
          status: interrupted ? 'failed' : value.status,
          endedAt: interrupted ? new Date().toISOString() : value.endedAt,
          recoveryPending: interrupted || value.recoveryPending === true,
          child: null,
        });
      }
      await this.persist();
    })();
    return this.restorePromise;
  }

  persist() {
    const snapshot = {
      version: 1, tenantID: this.tenantID,
      sessions: [...this.sessions.values()].map(({ id, agentID, conversationID, meetURL, status, startedAt, expiresAt, endedAt, recoveryPending }) =>
        ({ id, agentID, conversationID, meetURL, status, startedAt, expiresAt, endedAt, recoveryPending: recoveryPending === true })),
    };
    this.pendingWrite = this.pendingWrite.catch(() => {}).then(async () => {
      await mkdir(this.profilesDir, { recursive: true });
      const temporary = `${this.statePath}.${randomUUID()}.tmp`;
      await writeFile(temporary, JSON.stringify(snapshot), { mode: 0o600 });
      await rename(temporary, this.statePath);
    });
    return this.pendingWrite;
  }

  async issuerPost(path, body, issuerToken, invocationToken, controlGrant) {
    const base = this.baseURL.endsWith('/') ? this.baseURL : `${this.baseURL}/`;
    const headers = { 'X-Tenant-ID': this.tenantID, 'Content-Type': 'application/json' };
    if (invocationToken) headers['X-Cortex-Meet-Invocation'] = secret(invocationToken, 'Cortex meeting invocation');
    else if (!controlGrant) headers.token = secret(issuerToken, 'Cortex meeting issuer token');
    const response = await this.fetchAPI(new URL(path, base), {
      method: 'POST',
      headers,
      body: JSON.stringify(controlGrant ? { ...body, controlGrant: secret(controlGrant, 'meeting control grant') } : body),
    });
    if (!response.ok) throw new Error(`Cortex authorization failed (${response.status})`);
    const envelope = await response.json();
    if (!envelope?.result) throw new Error('Cortex returned no result');
    return envelope.result;
  }

  async destination({ runID, agentID, issuerToken, invocationToken, action }) {
    const result = await this.issuerPost('actions/context', {
      runId: validID(runID, 'run ID'), agentId: validID(agentID, 'agent ID'), action,
    }, issuerToken, invocationToken);
    return validID(result.conversationId, 'conversation ID');
  }

  async recoverInterrupted({ session, runID, agentID, action, issuerToken, invocationToken }) {
    if (!session?.recoveryPending) return;
    await this.issuerPost('actions/recover', {
      runId: validID(runID, 'run ID'), agentId: agentID, action,
      sessionId: session.id, conversationId: session.conversationID,
      sessionExpiresAt: session.expiresAt,
    }, issuerToken, invocationToken);
    session.recoveryPending = false;
    await this.persist();
  }

  async start({ runID, agentID, url, issuerToken, invocationToken, speechToken, transcriptionModel, speechModel, voice, durationMinutes = 240 }) {
    const resolvedIssuerToken = issuerToken ? secret(issuerToken, 'Cortex meeting issuer token') : undefined;
    if (!invocationToken && !resolvedIssuerToken) throw new Error('Cortex meeting invocation is required');
    const resolvedSpeechToken = speechToken ? secret(speechToken, 'speech token') : undefined;
    required(this.speechConfig.AXIOM_SPEECH_API_URL, 'speech endpoint');
    const selectedTranscriptionModel = selection(transcriptionModel, this.speechConfig.AXIOM_TRANSCRIPTION_MODEL, 'transcription model', 200);
    const selectedSpeechModel = selection(speechModel, this.speechConfig.AXIOM_SPEECH_MODEL, 'speech model', 200);
    const selectedVoice = selection(voice, this.speechConfig.AXIOM_SPEECH_VOICE, 'speech voice', 100);
    if (!Number.isInteger(durationMinutes) || durationMinutes < 15 || durationMinutes > 480) {
      throw new Error('meeting duration must be between 15 and 480 minutes');
    }
    agentID = validID(agentID, 'agent ID');
    const meetURL = meetingURL(url);
    await this.restore();
    const intendedConversationID = await this.destination({ runID, agentID, issuerToken: resolvedIssuerToken,
      invocationToken, action: invocationToken ? 'meet-start' : 'meet-status' });
    const current = this.sessions.get(agentID);
    if (current && !['ended', 'failed'].includes(current.status)) {
      if (current.conversationID === intendedConversationID && current.meetURL === meetURL) return this.publicState(current);
      throw new Error('this Agent already has an active meeting');
    }
    await this.recoverInterrupted({ session: current, runID, agentID, action: 'meet-start',
      issuerToken: resolvedIssuerToken, invocationToken });
    const issued = await this.issuerPost('grants', {
      runId: validID(runID, 'run ID'), agentId: agentID, durationMinutes,
    }, resolvedIssuerToken, invocationToken);
    const conversationID = validID(issued.conversationId, 'conversation ID');
    if (conversationID !== intendedConversationID) throw new Error('meeting destination changed during authorization');
    const sessionID = validID(issued.sessionId, 'meeting session ID');
    const grant = secret(issued.grant, 'meeting session grant');
    const controlGrant = issued.controlGrant ? secret(issued.controlGrant, 'meeting control grant') : undefined;
    if (!controlGrant && !resolvedIssuerToken) throw new Error('meeting control grant is required');
    const expiresAt = required(issued.sessionExpiresAt, 'meeting expiry');
    const grantExpiresAt = required(issued.grantExpiresAt, 'meeting grant expiry');
    if (!Number.isFinite(Date.parse(expiresAt)) || !Number.isFinite(Date.parse(grantExpiresAt)) ||
        Date.parse(expiresAt) <= Date.now() || Date.parse(grantExpiresAt) <= Date.now()) {
      throw new Error('meeting issuer returned an invalid expiry');
    }
    const startedAt = new Date();
    const session = {
      id: sessionID, agentID, conversationID, meetURL, status: 'joining',
      startedAt: startedAt.toISOString(), expiresAt,
      grant, grantExpiresAt, issuerToken: resolvedIssuerToken, controlGrant,
      transcriptionModel: selectedTranscriptionModel, speechModel: selectedSpeechModel,
      speechManaged: !resolvedSpeechToken,
      endedAt: null, child: null,
    };
    try {
      if (session.speechManaged) await this.refreshSpeechGrants(session);
      else session.transcriptionToken = session.speechToken = resolvedSpeechToken;
    } catch (error) {
      await this.revoke(session).catch(() => {});
      throw error;
    }
    const childEnv = {
        ...process.env,
        ...this.speechConfig,
        CORTEX_MEET_GRANT: grant,
        AXIOM_TRANSCRIPTION_TOKEN: session.transcriptionToken,
        AXIOM_SPEECH_TOKEN: session.speechToken,
        AXIOM_TRANSCRIPTION_MODEL: selectedTranscriptionModel,
        AXIOM_SPEECH_MODEL: selectedSpeechModel,
        AXIOM_SPEECH_VOICE: selectedVoice,
        MEET_URL: meetURL,
        MEET_SESSION_EXPIRES_AT: session.expiresAt,
        MEET_SESSION_ID: session.id,
        CORTEX_MEET_SESSION_API_URL: new URL(`sessions/${encodeURIComponent(session.id)}/`,
          this.baseURL.endsWith('/') ? this.baseURL : `${this.baseURL}/`).toString(),
        CORTEX_TENANT_ID: this.tenantID,
        CORTEX_AGENT_ID: agentID,
        CORTEX_CONVERSATION_ID: conversationID,
        GOOGLE_PROFILE_DIR: join(this.profilesDir, agentID),
    };
    delete childEnv.CORTEX_MEET_ISSUER_TOKEN;
    delete childEnv.CORTEX_MEET_INVOCATION;
    delete childEnv.CORTEX_MEET_CONTROL_GRANT;
    delete childEnv.CORTEX_BOT_TOKEN;
    let child;
    try {
      child = this.forkProcess(new URL('./main.mjs', import.meta.url), [], {
        stdio: ['ignore', 'inherit', 'inherit', 'ipc'],
        env: childEnv,
      });
    } catch (error) {
      await this.revoke(session).catch(() => {});
      throw error;
    }
    session.child = child;
    this.sessions.set(agentID, session);
    session.expiryTimer = setTimeout(() => { void this.beginStop(session); }, Date.parse(session.expiresAt) - Date.now());
    session.expiryTimer.unref();
    this.scheduleRenewal(session);
    this.scheduleSpeechRenewal(session);
    child.on('message', message => {
      if ((message?.status === 'awaiting_admission' && session.status === 'joining') ||
          (message?.status === 'active' && ['joining', 'awaiting_admission'].includes(session.status))) {
        session.status = message.status;
        void this.persist().catch(() => {});
      }
    });
    child.once('error', () => {
      this.clearSessionTimers(session);
      session.status = 'failed'; session.endedAt = new Date().toISOString();
      session.child = null;
      void this.persist().catch(() => {});
      void this.reportOutcome(session).catch(() => {});
    });
    child.once('exit', code => {
      this.clearSessionTimers(session);
      session.status = session.renewFailed || session.status === 'failed' ? 'failed' : session.status === 'leaving' || code === 0 ? 'ended' : 'failed';
      session.endedAt = new Date().toISOString();
      session.child = null;
      void this.persist().catch(() => {});
      void this.reportOutcome(session).catch(() => {});
    });
    try {
      await this.persist();
    } catch (error) {
      this.beginStop(session);
      throw error;
    }
    return this.publicState(session);
  }

  async status({ runID, agentID, issuerToken, invocationToken }) {
    agentID = validID(agentID, 'agent ID');
    const conversationID = await this.destination({ runID, agentID, issuerToken, invocationToken, action: 'meet-status' });
    await this.restore();
    await this.pendingWrite;
    const session = this.sessions.get(agentID);
    if (session?.conversationID === conversationID && session.recoveryPending) {
      // Status can also be requested by an untrusted meeting observation.
      // Sentinel permits recovery only from a human-triggered status Run.
      await this.recoverInterrupted({ session, runID, agentID, action: 'meet-status',
        issuerToken, invocationToken }).catch(() => {});
    }
    return session?.conversationID === conversationID ? this.publicState(session) : { status: 'none' };
  }

  async models({ runID, agentID, issuerToken, invocationToken, speechToken }) {
    agentID = validID(agentID, 'agent ID');
    await this.destination({ runID, agentID, issuerToken, invocationToken, action: 'meet-models' });
    const token = speechToken || (await this.issuerPost('actions/speech/catalog-grant', {
      runId: validID(runID, 'run ID'), agentId: agentID,
    }, issuerToken, invocationToken)).token;
    return availableSpeechModels({ baseURL: this.speechConfig.AXIOM_SPEECH_API_URL,
      token: secret(token, 'speech token'), fetchAPI: this.fetchAPI });
  }

  async stop({ runID, agentID, issuerToken, invocationToken }) {
    agentID = validID(agentID, 'agent ID');
    const conversationID = await this.destination({ runID, agentID, issuerToken, invocationToken, action: 'meet-stop' });
    await this.restore();
    const session = this.sessions.get(agentID);
    if (!session || session.conversationID !== conversationID) return { status: 'none' };
    await this.recoverInterrupted({ session, runID, agentID, action: 'meet-stop',
      issuerToken, invocationToken });
    const stopping = this.beginStop(session);
    await this.revoke(session);
    if (stopping) await this.pendingWrite;
    return this.publicState(session);
  }

  clearSessionTimers(session) {
    clearTimeout(session.expiryTimer);
    clearTimeout(session.killTimer);
    clearTimeout(session.renewTimer);
    clearTimeout(session.speechTimer);
    session.expiryTimer = null;
    session.killTimer = null;
    session.renewTimer = null;
    session.speechTimer = null;
  }

  async revoke(session) {
    if ((!session.controlGrant && !session.issuerToken) || !session.grant) return;
    if (!session.revokePromise) {
      session.revokePromise = this.issuerPost(`sessions/${encodeURIComponent(session.id)}/revoke`,
        { grant: session.grant }, session.issuerToken, undefined, session.controlGrant).catch(error => {
        session.revokePromise = null;
        throw error;
      });
    }
    await session.revokePromise;
  }

  async reportOutcome(session) {
    if ((!session.controlGrant && !session.issuerToken) || !session.grant) return;
    await this.revoke(session);
    if (!session.outcomePromise) {
      session.outcomePromise = this.issuerPost(`sessions/${encodeURIComponent(session.id)}/outcome`,
        { grant: session.grant, outcome: session.status === 'failed' ? 'failed' : 'ended' },
        session.issuerToken, undefined, session.controlGrant).catch(error => {
        session.outcomePromise = null;
        throw error;
      });
    }
    await session.outcomePromise;
  }

  scheduleRenewal(session) {
    if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
    if (Date.parse(session.grantExpiresAt) >= Date.parse(session.expiresAt)) return;
    const delay = Math.max(1000, Date.parse(session.grantExpiresAt) - Date.now() - 60000);
    session.renewTimer = setTimeout(() => { void this.renew(session); }, delay);
    session.renewTimer.unref();
  }

  async renew(session) {
    if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
    try {
      const result = await this.issuerPost('grants/renew', { previousGrant: session.grant },
        session.issuerToken, undefined, session.controlGrant);
      if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
      session.grant = secret(result.grant, 'renewed meeting grant');
      session.grantExpiresAt = required(result.grantExpiresAt, 'renewed grant expiry');
      if (!Number.isFinite(Date.parse(session.grantExpiresAt)) || Date.parse(session.grantExpiresAt) <= Date.now()) {
        throw new Error('renewed meeting grant has expired');
      }
      session.child.send({ type: 'grant', grant: session.grant });
      this.scheduleRenewal(session);
    } catch {
      session.renewFailed = true;
      this.beginStop(session);
    }
  }

  async refreshSpeechGrants(session) {
    const result = await this.issuerPost(`sessions/${encodeURIComponent(session.id)}/speech/grants`, {
      grant: session.grant, transcriptionModel: session.transcriptionModel, speechModel: session.speechModel,
    }, session.issuerToken, undefined, session.controlGrant);
    session.transcriptionToken = secret(result.transcriptionToken, 'transcription token');
    session.speechToken = secret(result.speechToken, 'speech token');
    session.speechGrantExpiresAt = required(result.expiresAt, 'speech grant expiry');
    if (!Number.isFinite(Date.parse(session.speechGrantExpiresAt)) || Date.parse(session.speechGrantExpiresAt) <= Date.now()) {
      throw new Error('speech grant has expired');
    }
  }

  scheduleSpeechRenewal(session) {
    if (!session.speechManaged || !session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
    if (Date.parse(session.speechGrantExpiresAt) >= Date.parse(session.expiresAt)) return;
    const delay = Math.max(1000, Date.parse(session.speechGrantExpiresAt) - Date.now() - 90000);
    session.speechTimer = setTimeout(() => { void this.renewSpeech(session); }, delay);
    session.speechTimer.unref();
  }

  async renewSpeech(session) {
    if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
    try {
      await this.refreshSpeechGrants(session);
      if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return;
      session.child.send({ type: 'speech-grants', transcriptionToken: session.transcriptionToken,
        speechToken: session.speechToken });
      this.scheduleSpeechRenewal(session);
    } catch {
      session.renewFailed = true;
      this.beginStop(session);
    }
  }

  beginStop(session) {
    if (!session.child || ['ended', 'failed', 'leaving'].includes(session.status)) return false;
    session.status = 'leaving';
    void this.revoke(session).catch(() => {});
    clearTimeout(session.expiryTimer);
    session.expiryTimer = null;
    session.child.kill('SIGTERM');
    // Browser teardown can hang. Do not leave a participant attached forever.
    session.killTimer = setTimeout(() => {
      if (session.child) session.child.kill('SIGKILL');
    }, this.terminationGraceMs);
    session.killTimer.unref();
    void this.persist().catch(() => {});
    return true;
  }

  publicState(session) {
    return { sessionId: session.id, status: session.status, meetURL: session.meetURL,
      startedAt: session.startedAt, expiresAt: session.expiresAt, endedAt: session.endedAt };
  }

  close() {
    for (const session of this.sessions.values()) {
      this.beginStop(session);
    }
  }

  ready() {
    return Boolean(this.speechConfig.AXIOM_SPEECH_API_URL?.trim());
  }
}
