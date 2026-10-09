import { fileURLToPath } from 'node:url';
import { addBrowserControlService, browserAuthorizer, BrowserSessionAPI, grpc, PersistentBrowserProfile, profileAuthorizer, PROFILE_ROOT, protoLoader } from '@axiom/live-browser';
import { schemas, validateInput } from './actions.mjs';
import { LiveBrowserService } from './service.mjs';

export const SKILL_ID = 'skill-live-browser';
export const SKILL_VERSION = '1.0.10';
export const DEFAULT_BROWSER_API_URL = 'http://sentinel.axiomcd.svc.cluster.local/orchestrator/agent/browser/v1/';

function decode(values = {}) {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => {
    const raw = Buffer.from(value).toString('utf8');
    try { return [key, JSON.parse(raw)]; } catch { return [key, raw]; }
  }));
}

function encode(values) {
  return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, Buffer.from(JSON.stringify(value))]));
}

export function handlers(service) {
  return {
    async Execute(call, callback) {
      const { node_type: action, context } = call.request;
      if (!schemas[action]) {
        callback(null, { error: { type: 'validation', message: 'unknown live browser action' } });
        return;
      }
      try {
        const input = validateInput(action, decode(call.request.config));
        // Bindings carry host invocation grants. They are
        // passed through to the service and never logged or returned.
        const bindings = decode(call.request.bindings);
        const result = await service.execute(action, { runID: context?.run_id, agentID: context?.agent_id, input, bindings });
        callback(null, { output: encode(result) });
      } catch (error) {
        callback(null, { error: { type: 'execution', message: error instanceof Error ? error.message.slice(0, 500) : 'Live browser action failed' } });
      }
    },
    GetNodeTypes(_call, callback) { callback(null, { node_types: Object.keys(schemas) }); },
    GetNodeSchema(call, callback) {
      const schema = schemas[call.request.node_type];
      if (!schema) {
        callback({ code: grpc.status.NOT_FOUND, message: 'unknown live browser action' });
        return;
      }
      callback(null, { schema: Buffer.from(JSON.stringify(schema)) });
    },
    Health(_call, callback) { callback(null, { healthy: service.ready?.() ?? true, skill_id: SKILL_ID, version: SKILL_VERSION }); },
  };
}

// SIGTERM (pod stop) or SIGINT: close every browser so Firefox flushes its
// profile and exits cleanly, then stop serving and exit. Bounded so the pod
// stops within its termination grace period even if a browser hangs.
export function gracefulShutdown({ service, server, exit = code => process.exit(code), timeoutMs = 45000 }) {
  let started = false;
  return () => {
    if (started) return;
    started = true;
    const timer = setTimeout(() => {
      console.warn(JSON.stringify({ event: 'live_browser_shutdown_timeout' }));
      server.forceShutdown();
      exit(1);
    }, timeoutMs);
    timer.unref?.();
    void Promise.resolve().then(() => service.closeAll()).catch(() => {}).finally(() => {
      clearTimeout(timer);
      server.forceShutdown();
      exit(0);
    });
  };
}

export async function serve(env = process.env) {
  const baseURL = env.CORTEX_BROWSER_API_URL || DEFAULT_BROWSER_API_URL;
  const service = new LiveBrowserService({
    api: new BrowserSessionAPI({ baseURL }),
    authorize: browserAuthorizer({ baseURL }),
    authorizeProfile: profileAuthorizer({ baseURL }),
    // Set by Cortex's Skill hosting for this tenant's runtime.
    tenantID: env.CORTEX_TENANT_ID,
    profile: new PersistentBrowserProfile({ root: env.LIVE_BROWSER_PROFILE_ROOT || PROFILE_ROOT }),
    ...(env.AXIOM_SPEECH_API_URL ? { speechBaseURL: env.AXIOM_SPEECH_API_URL } : {}),
  });
  const definition = protoLoader.loadSync(fileURLToPath(new URL('./skill.proto', import.meta.url)), { keepCase: true });
  const server = new grpc.Server();
  server.addService(grpc.loadPackageDefinition(definition).axiom.skill.v1.SkillService.service, handlers(service));
  addBrowserControlService(server, service);
  const port = Number(env.SKILL_PORT || 50051);
  await new Promise((resolve, reject) => server.bindAsync(`0.0.0.0:${port}`, grpc.ServerCredentials.createInsecure(),
    (error, bound) => error ? reject(error) : resolve(bound)));
  const shutdown = gracefulShutdown({ service, server });
  process.once('SIGTERM', shutdown);
  process.once('SIGINT', shutdown);
  return server;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) serve().catch(error => {
  console.error('Live browser Skill failed:', error.name);
  process.exitCode = 1;
});
