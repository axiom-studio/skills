import grpc from '@grpc/grpc-js';
import protoLoader from '@grpc/proto-loader';
import { fileURLToPath } from 'node:url';
import { MeetSessionService } from './session.mjs';

export const SKILL_ID = 'openseal.meeting.voice';
export const SKILL_VERSION = '0.2.4';

const schemas = {
  'meet-start': { type: 'object', additionalProperties: false, required: ['url'], properties: {
    url: { type: 'string', minLength: 25, maxLength: 2048 },
    transcriptionModel: { type: 'string', minLength: 1, maxLength: 200 },
    speechModel: { type: 'string', minLength: 1, maxLength: 200 },
    voice: { type: 'string', minLength: 1, maxLength: 100 },
    durationMinutes: { type: 'integer', minimum: 15, maximum: 480, default: 240 },
  } },
  'meet-models': { type: 'object', additionalProperties: false },
  'meet-status': { type: 'object', additionalProperties: false },
  'meet-stop': { type: 'object', additionalProperties: false },
};

function decode(values = {}) {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => {
    const raw = Buffer.from(value).toString('utf8');
    try { return [key, JSON.parse(raw)]; } catch { return [key, raw]; }
  }));
}

function encode(values) {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, Buffer.from(JSON.stringify(value))]));
}

function meetingInvocation(binding) {
  if (binding === undefined) return undefined;
  try {
    const grants = typeof binding === 'string' ? JSON.parse(binding) : binding;
    if (!grants || typeof grants !== 'object' || Array.isArray(grants)) throw new Error();
    const token = grants['host:meet'];
    if (typeof token !== 'string' || !token || token.length > 16384) throw new Error();
    return token;
  } catch {
    // Parser diagnostics may contain the secret input. Never return them.
    throw new Error('Invalid host invocation binding');
  }
}

export function handlers(service) {
  return {
    async Execute(call, callback) {
      const { node_type: action, context } = call.request;
      if (!schemas[action]) {
        callback(null, { error: { type: 'validation', message: 'unknown Meet action' } });
        return;
      }
      try {
        const input = decode(call.request.config);
        const bindings = decode(call.request.bindings);
        const invocationToken = meetingInvocation(bindings.CORTEX_HOST_INVOCATIONS);
        // Current manifests use the Vault field slot. Keep older installations
        // working while they still send the legacy type-name slot.
        const elevenLabsAPIKey = bindings.api_key || bindings.elevenlabs_api;
        const common = { runID: context?.run_id, agentID: context?.agent_id };
        const result = action === 'meet-start'
          ? await service.start({ ...common, url: input.url, issuerToken: bindings.CORTEX_MEET_ISSUER_TOKEN,
            invocationToken,
            speechToken: bindings.AXIOM_SPEECH_TOKEN,
            ...(elevenLabsAPIKey ? { elevenLabsAPIKey } : {}),
            transcriptionModel: input.transcriptionModel,
            speechModel: input.speechModel, voice: input.voice, durationMinutes: input.durationMinutes })
          : action === 'meet-models' ? await service.models({ ...common, issuerToken: bindings.CORTEX_MEET_ISSUER_TOKEN,
            invocationToken,
            speechToken: bindings.AXIOM_SPEECH_TOKEN,
            ...(elevenLabsAPIKey ? { elevenLabsAPIKey } : {}) })
          : action === 'meet-stop' ? await service.stop({ ...common, issuerToken: bindings.CORTEX_MEET_ISSUER_TOKEN,
            invocationToken })
            : await service.status({ ...common, issuerToken: bindings.CORTEX_MEET_ISSUER_TOKEN,
              invocationToken });
        callback(null, { output: encode(result) });
      } catch (error) {
        callback(null, { error: { type: 'execution', message: error instanceof Error ? error.message : 'Meet action failed' } });
      }
    },
    GetNodeTypes(_call, callback) { callback(null, { node_types: Object.keys(schemas) }); },
    GetNodeSchema(call, callback) {
      const schema = schemas[call.request.node_type];
      if (!schema) {
        callback({ code: grpc.status.NOT_FOUND, message: 'unknown Meet action' });
        return;
      }
      callback(null, { schema: Buffer.from(JSON.stringify(schema)) });
    },
    Health(_call, callback) { callback(null, { healthy: service.ready?.() ?? true, skill_id: SKILL_ID, version: SKILL_VERSION }); },
  };
}

export async function serve() {
  const service = new MeetSessionService({
    baseURL: process.env.CORTEX_MEET_API_URL,
    tenantID: process.env.CORTEX_TENANT_ID,
    profilesDir: process.env.GOOGLE_PROFILES_DIR || '/profile',
  });
  const definition = protoLoader.loadSync(fileURLToPath(new URL('./skill.proto', import.meta.url)), { keepCase: true });
  const protocol = grpc.loadPackageDefinition(definition).axiom.skill.v1;
  const server = new grpc.Server();
  server.addService(protocol.SkillService.service, handlers(service));
  const port = Number(process.env.SKILL_PORT || 50051);
  await new Promise((resolve, reject) => server.bindAsync(`0.0.0.0:${port}`, grpc.ServerCredentials.createInsecure(),
    (error, bound) => error ? reject(error) : resolve(bound)));
  const shutdown = () => { service.close(); server.tryShutdown(() => {}); };
  process.once('SIGTERM', shutdown);
  process.once('SIGINT', shutdown);
  return server;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) serve().catch(error => {
  console.error('Meet Skill failed:', error.name);
  process.exitCode = 1;
});
