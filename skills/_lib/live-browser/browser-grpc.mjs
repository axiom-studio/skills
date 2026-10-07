import grpc from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';
import { fileURLToPath } from 'node:url';
import { browserVideoRPC } from './browser-video-rpc.mjs';

export { grpc, protoLoader };

const FIELDS = ['agentID', 'sessionID', 'authorization', 'commandJSON'];

// Host-only BrowserControlService. A Skill's model actions never reach these
// handlers; every request carries a human proof that the service verifies.
export function browserHandlers(service) {
  return {
    Video(call) { browserVideoRPC(service, call); },
    Desktop(call) { browserVideoRPC(service, call, true); },
    async Control(call, callback) {
      try {
        const bytes = call.request.value;
        if (!bytes || bytes.length > 49152) throw new Error();
        const input = JSON.parse(Buffer.from(bytes).toString('utf8'));
        if (!input || Array.isArray(input) || Object.keys(input).some(key => !FIELDS.includes(key)) ||
          typeof input.commandJSON !== 'string') throw new Error();
        const result = await service.controlBrowser({ agentID: input.agentID, sessionID: input.sessionID,
          authorization: { token: input.authorization, commandJSON: input.commandJSON }, command: JSON.parse(input.commandJSON) });
        if (result?.type === 'frame' && Buffer.isBuffer(result.bytes)) {
          callback(null, { value: Buffer.from(JSON.stringify({ ...result, bytes: result.bytes.toString('base64') })) });
          return;
        }
        callback(null, { value: Buffer.from(JSON.stringify(result)) });
      } catch { callback({ code: grpc.status.PERMISSION_DENIED, message: 'Browser control request could not be completed' }); }
    },
  };
}

export function loadBrowserControlService() {
  const definition = protoLoader.loadSync(fileURLToPath(new URL('./browser-control.proto', import.meta.url)), { keepCase: true });
  return grpc.loadPackageDefinition(definition).axiom.browser.v1.BrowserControlService.service;
}

export function addBrowserControlService(server, service) {
  server.addService(loadBrowserControlService(), browserHandlers(service));
}
