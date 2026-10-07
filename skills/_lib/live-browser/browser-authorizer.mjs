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
