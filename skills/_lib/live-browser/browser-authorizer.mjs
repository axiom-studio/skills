import { createHash } from 'node:crypto';

// The signed command proof comes from a human-authenticated host request. The
// worker supplies its own browser-runtime grant independently. Neither grant alone
// grants access to the browser, and the host rechecks durable session state.
export function browserAuthorizer({ baseURL, fetchAPI = fetch }) {
  const base = new URL(baseURL.endsWith('/') ? baseURL : `${baseURL}/`);
  return async ({ authorization, command, tenantID, agentID, sessionID, sessionGrant }) => {
    try {
      if (typeof authorization?.token !== 'string' || authorization.token.length > 8192 ||
        typeof authorization.commandJSON !== 'string' || Buffer.byteLength(authorization.commandJSON) > 32768 ||
        JSON.stringify(JSON.parse(authorization.commandJSON)) !== JSON.stringify(command) || !sessionGrant) throw new Error();
      const commandDigest = createHash('sha256').update(authorization.commandJSON).digest('hex');
      const response = await fetchAPI(new URL(`sessions/${encodeURIComponent(sessionID)}/authorize`, base), {
        method: 'POST', redirect: 'error', signal: AbortSignal.timeout(5000),
        headers: { Authorization: `Bearer ${sessionGrant}`, 'Content-Type': 'application/json', 'X-Tenant-ID': tenantID },
        body: JSON.stringify({ authorization: authorization.token, commandDigest }),
      });
      if (!response.ok) throw new Error();
      const encoded = await response.text();
      if (Buffer.byteLength(encoded) > 8192) throw new Error();
      const { result } = JSON.parse(encoded);
      if (result?.tenantID !== tenantID || result.agentID !== agentID || typeof result.userID !== 'string' ||
        !result.userID || typeof result.requestID !== 'string' || !result.requestID ||
        !Number.isFinite(Date.parse(result.expiresAt)) || Date.parse(result.expiresAt) <= Date.now()) throw new Error();
      return result;
    } catch { throw new Error('Browser control authorization failed'); }
  };
}

// Profile commands (profileStatus, forgetProfile) concern the tenant's
// browser profile, not one browser session, so they work with no browser
// open. The proof is a short-lived, single-command grant Cortex signed for an
// authenticated human; Cortex verifies it (exact command digest, live tenant
// membership) without a session grant. The runtime then requires the proof's
// tenant to be the tenant this runtime was deployed for.
export const PROFILE_COMMANDS = Object.freeze(['profileStatus', 'forgetProfile']);

export function profileAuthorizer({ baseURL, fetchAPI = fetch }) {
  const base = new URL(baseURL.endsWith('/') ? baseURL : `${baseURL}/`);
  return async ({ authorization, command, tenantID }) => {
    try {
      if (typeof tenantID !== 'string' || !/^[1-9][0-9]{0,18}$/.test(tenantID) ||
        typeof authorization?.token !== 'string' || authorization.token.length > 8192 ||
        typeof authorization.commandJSON !== 'string' || Buffer.byteLength(authorization.commandJSON) > 1024 ||
        !PROFILE_COMMANDS.includes(command?.type) || Object.keys(command).length !== 1 ||
        JSON.stringify(JSON.parse(authorization.commandJSON)) !== JSON.stringify(command)) throw new Error();
      const commandDigest = createHash('sha256').update(authorization.commandJSON).digest('hex');
      const response = await fetchAPI(new URL('profile/authorize', base), {
        method: 'POST', redirect: 'error', signal: AbortSignal.timeout(5000),
        headers: { 'Content-Type': 'application/json', 'X-Tenant-ID': tenantID },
        body: JSON.stringify({ authorization: authorization.token, commandDigest }),
      });
      if (!response.ok) throw new Error();
      const encoded = await response.text();
      if (Buffer.byteLength(encoded) > 8192) throw new Error();
      const { result } = JSON.parse(encoded);
      if (result?.tenantID !== tenantID || typeof result.agentID !== 'string' || !result.agentID ||
        typeof result.userID !== 'string' || !result.userID || typeof result.requestID !== 'string' || !result.requestID ||
        !Number.isFinite(Date.parse(result.expiresAt)) || Date.parse(result.expiresAt) <= Date.now()) throw new Error();
      return result;
    } catch { throw new Error('Browser profile authorization failed'); }
  };
}
