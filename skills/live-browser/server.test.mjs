import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';
import { grpc, protoLoader } from '@axiom/live-browser';
import { fileURLToPath } from 'node:url';
import { schemas, validateInput } from './actions.mjs';
import { CARD_SLOT, LOGIN_SLOTS } from './credentials.mjs';
import { gracefulShutdown, handlers, SKILL_ID, SKILL_VERSION } from './server.mjs';

const invoke = (handler, request) => new Promise((resolve, reject) => handler({ request },
  (error, result) => error ? reject(error) : resolve(result)));
const manifest = readFileSync(new URL('./skill.yaml', import.meta.url), 'utf8');

test('the action catalog, identity and version match skill.yaml', () => {
  const declared = [...manifest.matchAll(/^    (live-browser-[a-z-]+):$/gm)].map(match => match[1]);
  assert.deepEqual(declared.sort(), Object.keys(schemas).sort());
  assert.match(manifest, new RegExp(`^  id: ${SKILL_ID}$`, 'm'));
  assert.match(manifest, new RegExp(`^  version: ${SKILL_VERSION.replaceAll('.', '\\.')}$`, 'm'));
  assert.match(manifest, new RegExp(`package: axiomstudio/skill-live-browser:${SKILL_VERSION.replaceAll('.', '\\.')}`));
  assert.match(manifest, /permissions: \[browser:session, network:http, 'host:browser:session', 'host:browser:audio'\]/);
  assert.equal([...manifest.matchAll(/'host:browser:/g)].length, 2, 'only start declares host permissions');
  assert.doesNotMatch(manifest, /host:browser:profile/);
  assert.doesNotMatch(manifest, /elevenlabs|risk: external|durationMinutes/);
  // Only sign-in and card fill declare (optional) vault credential slots.
  assert.equal([...manifest.matchAll(/^      credentials:$/gm)].length, 2);
  const slots = [...manifest.matchAll(/- kind: (\S+)\n {10}name: (\S+)\n {10}optional: true/g)].map(match => `${match[1]}:${match[2]}`);
  assert.deepEqual(slots, [...LOGIN_SLOTS.map(slot => `http_basic_auth:${slot}`), `payment_card:${CARD_SLOT}`]);
  assert.doesNotMatch(manifest, /host:meet/);
  // actions.mjs and skill.yaml declare the same input fields and required fields.
  for (const [action, schema] of Object.entries(schemas)) {
    const block = manifest.slice(manifest.indexOf(`    ${action}:\n`)).split(/\n    live-browser-/)[0];
    const input = block.slice(block.indexOf('inputSchema:'), block.indexOf('outputSchema:') > 0 ? block.indexOf('outputSchema:') : block.indexOf('permissions:'));
    const fields = [...input.matchAll(/^ {10}('?)([A-Za-z]+)\1:/gm)].map(match => match[2]);
    assert.deepEqual(fields.sort(), Object.keys(schema.properties).sort(), action);
    const required = /^ {8}required: \[([^\]]*)\]/m.exec(input)?.[1].split(',').map(name => name.trim().replace(/'/g, '')).filter(Boolean) ?? [];
    assert.deepEqual(required.sort(), [...schema.required].sort(), action);
  }
  const packageJSON = JSON.parse(readFileSync(new URL('./package.json', import.meta.url)));
  assert.equal(packageJSON.version, SKILL_VERSION);
});

test('Execute validates input, passes bindings privately and never echoes them', async () => {
  const seen = [];
  const service = { execute: async (action, request) => { seen.push([action, request]); return { sessionId: 'b-1', status: 'automating' }; } };
  const encode = value => Buffer.from(JSON.stringify(value));
  const ok = await invoke(handlers(service).Execute, { node_type: 'live-browser-navigate', context: { run_id: 'run-1', agent_id: 'agent-1' },
    config: { sessionId: encode('b-1'), url: encode('https://example.com/') }, bindings: { CORTEX_HOST_INVOCATIONS: encode('{"host:browser":"secret"}') } });
  assert.equal(JSON.parse(ok.output.status), 'automating');
  assert.equal(seen[0][1].agentID, 'agent-1');
  assert.equal(seen[0][1].bindings.CORTEX_HOST_INVOCATIONS, '{"host:browser":"secret"}');
  const unknown = await invoke(handlers(service).Execute, { node_type: 'camoufox-start' });
  assert.equal(unknown.error.message, 'unknown live browser action');
  const invalid = await invoke(handlers(service).Execute, { node_type: 'live-browser-navigate', context: {},
    config: { sessionId: encode('b-1'), url: encode('file:///etc/passwd') }, bindings: { api_key: encode('vault-secret') } });
  assert.match(invalid.error.message, /url is invalid/);
  const failing = { execute: async () => { throw new Error('The browser action failed'); } };
  const failed = await invoke(handlers(failing).Execute, { node_type: 'live-browser-close', context: {}, config: { sessionId: encode('b-1') },
    bindings: { api_key: encode('vault-secret') } });
  assert.doesNotMatch(JSON.stringify(failed), /vault-secret/);
  const health = await invoke(handlers(service).Health, {});
  assert.deepEqual(health, { healthy: true, skill_id: SKILL_ID, version: SKILL_VERSION });
  const types = await invoke(handlers(service).GetNodeTypes, {});
  assert.equal(types.node_types.length, 14);
});

test('input validation enforces the declared shapes', () => {
  assert.throws(() => validateInput('live-browser-click', { sessionId: 'b-1', intent: 'Click it' }), /either target/);
  assert.throws(() => validateInput('live-browser-click', { sessionId: 'b-1', intent: 'Click it', target: 's1:e1', generation: 1, x: 1, y: 1 }), /either target/);
  assert.throws(() => validateInput('live-browser-request-handoff', { sessionId: 'b-1', reason: 'whatever', summary: 'x' }), /reason is invalid/);
  assert.throws(() => validateInput('live-browser-fill', { sessionId: 'b-1', target: 's1:e1', value: 'x', intent: 'Type', extra: 1 }), /unknown field/);
  assert.throws(() => validateInput('live-browser-listen', { sessionId: 'b-1', state: 'maybe' }), /state is invalid/);
  assert.throws(() => validateInput('live-browser-scroll', { sessionId: 'b-1', dy: 20000 }), /out of range/);
  assert.equal(validateInput('live-browser-listen', { sessionId: 'b-1', state: 'on', wakePhrases: ['Ada'] }).state, 'on');
  assert.throws(() => validateInput('live-browser-start', { durationMinutes: 30 }), /unknown field/);
  assert.throws(() => validateInput('live-browser-sign-in', { sessionId: 'b-1', password: 'x' }), /unknown field/);
  assert.throws(() => validateInput('live-browser-sign-in', { sessionId: 'b-1', oneTimeCode: '12' }), /oneTimeCode is invalid/);
  assert.equal(validateInput('live-browser-sign-in', { sessionId: 'b-1', oneTimeCode: '123 456' }).oneTimeCode, '123 456');
  assert.throws(() => validateInput('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '10' }), /currency is required/);
  assert.throws(() => validateInput('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '1,000', currency: 'INR' }), /amount is invalid/);
  assert.throws(() => validateInput('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '10', currency: 'inr' }), /currency is invalid/);
  assert.throws(() => validateInput('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '10', currency: 'INR', number: '4242' }), /unknown field/);
  assert.equal(validateInput('live-browser-fill-payment-card', { sessionId: 'b-1', amount: '4210.50', currency: 'INR' }).amount, '4210.50');
});

test('skill and browser-control protocols load', () => {
  const definition = protoLoader.loadSync(fileURLToPath(new URL('./skill.proto', import.meta.url)), { keepCase: true });
  assert.ok(grpc.loadPackageDefinition(definition).axiom.skill.v1.SkillService.service.Execute);
});

test('the browser profile is declared as one persistent single-writer volume at the runtime profile root', async () => {
  const { PROFILE_ROOT } = await import('@axiom/live-browser');
  const storage = manifest.slice(manifest.indexOf('    storage:'), manifest.indexOf('  prompt:'));
  assert.match(storage, /- name: browser-profile\n/);
  assert.match(storage, /durability: persistent\n/);
  assert.match(storage, /minimumCapacity: 10Gi\n/);
  assert.match(storage, new RegExp(`mountPath: ${PROFILE_ROOT}\\n`));
  assert.match(storage, /retention: retain\n/);
  assert.match(storage, /writableGroup: 1000\n/);
  assert.equal([...manifest.matchAll(/^      - name: /gm)].length, 1);
  const dockerfile = readFileSync(new URL('./Dockerfile', import.meta.url), 'utf8');
  assert.match(dockerfile, /ENTRYPOINT \["\/usr\/bin\/tini", "--", /, 'an init reaps orphaned browser processes');
});

test('SIGTERM closes every browser before the server stops and the process exits', async () => {
  const order = [];
  let finish;
  const service = { closeAll: () => { order.push('closeAll'); return new Promise(resolve => { finish = resolve; }); } };
  const server = { forceShutdown: () => order.push('forceShutdown') };
  const exited = new Promise(resolve => {
    const shutdown = gracefulShutdown({ service, server, exit: code => { order.push(`exit ${code}`); resolve(); }, timeoutMs: 1000 });
    shutdown();
    shutdown(); // a second signal does not start another shutdown
  });
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.deepEqual(order, ['closeAll'], 'nothing stops while Camoufox is still flushing');
  finish();
  await exited;
  assert.deepEqual(order, ['closeAll', 'forceShutdown', 'exit 0']);
  // A hung browser cannot hold the pod past its grace period.
  const stuck = [];
  await new Promise(resolve => gracefulShutdown({ service: { closeAll: () => new Promise(() => {}) }, server: { forceShutdown: () => stuck.push('force') },
    exit: code => { stuck.push(code); resolve(); }, timeoutMs: 20 })());
  assert.deepEqual(stuck, ['force', 1]);
});
