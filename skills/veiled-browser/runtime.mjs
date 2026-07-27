import { createHash } from "node:crypto";
import { mkdir } from "node:fs/promises";
import path from "node:path";
import { Browser, Fingerprint } from "@achamm/veilbrowser";

const ID = /^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/;
const SECRET_CONTROL = /(password|passcode|secret|token|api.?key|authorization|cookie|private.?key)/i;
const CHALLENGES = {
  captcha: /\b(captcha|recaptcha|hcaptcha|verify you are human)\b/i,
  mfa: /\b(multi[ -]?factor|two[ -]?factor|2fa|authenticator|verification code)\b/i,
  anti_bot: /\b(access denied|unusual traffic|bot detection|security check|cloudflare|js_challenge)\b/i,
};

function configuredMap(env, name, required = true) {
  const raw = env[name]?.trim();
  if (!raw) {
    if (required) throw new Error(`${name} is required`);
    return {};
  }
  let value;
  try { value = JSON.parse(raw); } catch { throw new Error(`${name} is invalid`); }
  if (!value || Array.isArray(value) || typeof value !== "object") throw new Error(`${name} must be an object`);
  return value;
}

export function loadInventory(env = process.env) {
  const requiredNames = ["VEILED_BROWSER_TARGETS", "VEILED_BROWSER_PROFILES", "VEILED_BROWSER_PROXY_POOLS"];
  if (requiredNames.every((name) => !env[name]?.trim())) return null;
  const inventory = {
    targets: configuredMap(env, "VEILED_BROWSER_TARGETS"),
    profiles: configuredMap(env, "VEILED_BROWSER_PROFILES"),
    proxyPools: configuredMap(env, "VEILED_BROWSER_PROXY_POOLS"),
    challenges: configuredMap(env, "VEILED_BROWSER_CHALLENGES", false),
  };
  for (const [id, target] of Object.entries(inventory.targets)) {
    let base;
    try { base = new URL(target.baseUrl); } catch { throw new Error(`target ${id} is invalid`); }
    if (!ID.test(id) || !["http:", "https:"].includes(base.protocol) || base.username || base.password) throw new Error(`target ${id} is invalid`);
    if (!Array.isArray(target.pathPrefixes) || target.pathPrefixes.length === 0 || target.pathPrefixes.some((prefix) => typeof prefix !== "string" || !prefix.startsWith("/"))) throw new Error(`target ${id} requires pathPrefixes`);
    if (!["owned-assessment", "permitted-automation"].includes(target.mode)) throw new Error(`target ${id} requires an explicit mode`);
    if (target.allowPrivateSubresources && target.mode !== "owned-assessment") throw new Error(`target ${id} cannot allow private subresources`);
  }
  for (const [id, profile] of Object.entries(inventory.profiles)) {
    if (!ID.test(id) || !profile || typeof profile !== "object") throw new Error(`profile ${id} is invalid`);
    if (profile.preset && !Fingerprint.presets[profile.preset]) throw new Error(`profile ${id} uses an unknown preset`);
    if (profile.seed != null && (!Number.isInteger(profile.seed) || profile.seed < 0)) throw new Error(`profile ${id} seed is invalid`);
    if (profile.preset && profile.seed != null) throw new Error(`profile ${id} cannot set preset and seed`);
  }
  for (const [id, proxy] of Object.entries(inventory.proxyPools)) {
    if (!ID.test(id) || !proxy || typeof proxy !== "object") throw new Error(`proxy pool ${id} is invalid`);
    const endpoints = proxyUrls(proxy);
    if (proxy.url && proxy.urls) throw new Error(`proxy pool ${id} cannot set url and urls`);
    if (proxy.urls && (!Array.isArray(proxy.urls) || proxy.urls.length === 0)) throw new Error(`proxy pool ${id} requires non-empty urls`);
    for (const endpoint of endpoints) { let parsed; try { parsed = new URL(endpoint); } catch { throw new Error(`proxy pool ${id} is invalid`); } if (!["http:", "https:", "socks5:"].includes(parsed.protocol)) throw new Error(`proxy pool ${id} is invalid`); }
  }
  for (const [id, challenge] of Object.entries(inventory.challenges)) {
    if (!ID.test(id) || challenge.kind !== "synthetic" || !inventory.targets[challenge.targetId] || inventory.targets[challenge.targetId].mode !== "owned-assessment" || typeof challenge.accessibleName !== "string" || !challenge.accessibleName) throw new Error(`challenge ${id} is invalid`);
  }
  if (!Object.keys(inventory.targets).length || !Object.keys(inventory.profiles).length || !Object.keys(inventory.proxyPools).length) throw new Error("assessment inventory must include a target, profile, and proxy pool");
  return inventory;
}

function exactPath(target, requested = "/") {
  if (typeof requested !== "string" || !requested.startsWith("/")) throw new Error("path must be relative to the authorized origin");
  const base = new URL(target.baseUrl);
  const destination = new URL(requested, base);
  if (destination.origin !== base.origin || !target.pathPrefixes.some((prefix) => destination.pathname.startsWith(prefix))) throw new Error("path is outside the authorized target scope");
  return destination.href;
}

function detectChallenges(text) {
  return Object.entries(CHALLENGES).filter(([, pattern]) => pattern.test(text)).map(([kind]) => kind);
}

function digest(...parts) { return `sha256:${createHash("sha256").update(parts.join("\0")).digest("hex")}`; }

function stableSeed(value) {
  const hex = createHash("sha256").update(String(value)).digest("hex").slice(0, 8);
  return Number.parseInt(hex, 16) >>> 0;
}

function proxyUrls(proxy) {
  if (Array.isArray(proxy?.urls)) return proxy.urls;
  if (proxy?.url) return [proxy.url];
  return [];
}

/** Resolve a coherent VeilBrowser fingerprint from an opaque profile inventory entry. */
export function resolveFingerprint(profile, profileId) {
  if (profile?.preset) {
    const fingerprint = Fingerprint.presets[profile.preset];
    if (!fingerprint) throw new Error("browser profile is unavailable");
    return fingerprint;
  }
  if (profile?.seed != null) return Fingerprint.random(profile.seed);
  return Fingerprint.random(stableSeed(profileId ?? "default"));
}

/** Rotate a proxy pool deterministically across sessions. */
export function resolveProxy(proxy, proxyPoolId, sessionId) {
  const endpoints = proxyUrls(proxy);
  if (!endpoints.length) return undefined;
  if (endpoints.length === 1) return endpoints[0];
  return endpoints[stableSeed(`${proxyPoolId}:${sessionId}`) % endpoints.length];
}

export class VeiledBrowserRuntime {
  constructor({ inventory, workspace, browserFactory = (options) => Browser.launch(options), now = () => new Date() }) {
    this.inventory = inventory;
    this.workspace = workspace;
    this.browserFactory = browserFactory;
    this.now = now;
    this.sessions = new Map();
  }

  async execute(action, config = {}, bindings = {}) {
    switch (action) {
      case "veiled-browser-health": return this.health();
      case "veiled-browser-start": return this.start(config);
      case "veiled-browser-snapshot": return this.snapshot(config, bindings);
      case "veiled-browser-click": return this.mutate("click", config);
      case "veiled-browser-commit": return this.mutate("click", config);
      case "veiled-browser-fill": return this.mutate("fill", config);
      case "veiled-browser-fill-secret": return this.fillSecret(config, bindings);
      case "veiled-browser-solve-synthetic": return this.solveSynthetic(config);
      case "veiled-browser-report": return this.report(config);
      case "veiled-browser-close": return this.close(config);
      default: throw new Error(`unsupported Veiled Browser action ${action}`);
    }
  }

  health() {
    if (!this.inventory) return { status: "needs_configuration", skillId: "skill-veiled-browser", version: "1.0.0", authorizedTargets: 0, profiles: 0, proxyPools: 0 };
    return { status: "ready", skillId: "skill-veiled-browser", version: "1.0.0", authorizedTargets: Object.keys(this.inventory.targets).length, profiles: Object.keys(this.inventory.profiles).length, proxyPools: Object.keys(this.inventory.proxyPools).length };
  }

  session(id) {
    if (!ID.test(id ?? "")) throw new Error("sessionId is invalid");
    const session = this.sessions.get(id);
    if (!session) throw new Error("assessment session not found");
    return session;
  }

  async start(config) {
    if (!this.inventory) throw new Error("Veiled Browser inventory is not configured");
    const { sessionId, targetId, profileId, proxyPoolId } = config;
    if (!ID.test(sessionId ?? "")) throw new Error("sessionId is invalid");
    if (this.sessions.has(sessionId)) throw new Error("assessment session already exists");
    const target = this.inventory.targets[targetId];
    const profile = this.inventory.profiles[profileId];
    const proxy = this.inventory.proxyPools[proxyPoolId];
    if (!target) throw new Error("authorized target is unavailable");
    if (!profile) throw new Error("browser profile is unavailable");
    if (!proxy) throw new Error("proxy pool is unavailable");
    if (target.mode === "permitted-automation" && (profile.assessmentOnly || proxy.assessmentOnly)) throw new Error("assessment-only identity or egress cannot be used with a third-party automation target");
    const destination = exactPath(target, config.path);
    const userDataDir = path.join(this.workspace, "profiles", profileId, sessionId);
    await mkdir(userDataDir, { recursive: true, mode: 0o700 });
    const fingerprint = resolveFingerprint(profile, profileId);
    const egress = resolveProxy(proxy, proxyPoolId, sessionId);
    const browser = await this.browserFactory({ headless: false, xvfb: true, userDataDir, proxy: egress, fingerprint, blockPrivateNetwork: !target.allowPrivateSubresources });
    const page = await browser.newPage();
    let navigation;
    try { navigation = await page.goto(destination, { timeout: 30_000, waitUntil: "networkidle" }); }
    catch (error) { await browser.close().catch(() => {}); throw error; }
    const session = { id: sessionId, targetId, profileId, proxyPoolId, target, browser, page, currentURL: destination, statusCode: navigation.status ?? 0, generation: 0, refs: new Map(), refNames: new Map(), challenges: [], snapshotText: "", mutations: 0, receipts: new Map(), secrets: new Set(), startedAt: this.now() };
    this.sessions.set(sessionId, session);
    return { sessionId, targetId, profileId, proxyPoolId, url: destination, status: "active" };
  }

  async snapshot(config, bindings = {}) {
    const session = this.session(config.sessionId);
    const raw = await session.page.snapshot();
    const secretValues = [...session.secrets, ...Object.values(bindings).filter((value) => typeof value === "string" && value.length >= 3)];
    const redact = (value) => secretValues.reduce((result, secret) => result.replaceAll(secret, "[REDACTED]"), String(value ?? ""));
    const text = redact(raw.text).slice(0, 200 << 10);
    session.generation += 1;
    session.refs.clear(); session.refNames.clear();
    const elements = raw.elements.slice(0, 500).map((element, index) => {
      const ref = `s${session.generation}:e${index + 1}`;
      session.refs.set(ref, element.ref); session.refNames.set(ref, element.name);
      return { ref, role: element.role, name: redact(element.name) };
    });
    session.currentURL = raw.url;
    session.snapshotText = text;
    const challenges = new Set(detectChallenges(text));
    if (session.target.mode === "owned-assessment") {
      const names = new Set(raw.elements.map((element) => element.name));
      for (const challenge of Object.values(this.inventory.challenges)) {
        if (challenge.targetId === session.targetId && names.has(challenge.accessibleName)) challenges.add("synthetic");
      }
    }
    session.challenges = [...challenges];
    return { sessionId: session.id, generation: session.generation, url: raw.url, title: raw.title, text, elements, challenges: session.challenges, requiresHuman: session.target.mode === "permitted-automation" && session.challenges.length > 0 };
  }

  async mutate(operation, config, secretValue) {
    const session = this.session(config.sessionId);
    if (session.target.mode === "permitted-automation" && session.challenges.length) throw new Error("the destination presented an access challenge; human completion is required");
    if (config.writeAuthorized !== true) throw new Error("writeAuthorized must be true");
    if (typeof config.idempotencyKey !== "string" || config.idempotencyKey.length < 8) throw new Error("idempotencyKey is required");
    const value = secretValue ?? config.value ?? "";
    const fingerprint = digest(operation, config.target, value);
    const receiptKey = `${operation}:${config.idempotencyKey}`;
    if (session.receipts.has(receiptKey)) {
      if (session.receipts.get(receiptKey) !== fingerprint) throw new Error("idempotency key was reused with different arguments");
      return { sessionId: session.id, duplicate: true, receipt: config.idempotencyKey };
    }
    const engineRef = session.refs.get(config.target);
    if (!engineRef) throw new Error("target is stale or unknown; take a new snapshot");
    if (operation === "fill" && secretValue === undefined && SECRET_CONTROL.test(session.refNames.get(config.target) ?? "")) throw new Error("literal values cannot fill a secret-like control; use an authorized credential binding");
    if (operation === "click") await session.page.click(engineRef);
    else await session.page.fill(engineRef, value);
    session.receipts.set(receiptKey, fingerprint);
    session.refs.clear(); session.refNames.clear(); session.mutations += 1;
    return { sessionId: session.id, success: true, duplicate: false, receipt: config.idempotencyKey };
  }

  async fillSecret(config, bindings) {
    const field = config.credentialField;
    const secret = typeof field === "string" ? bindings[field] : undefined;
    if (typeof secret !== "string" || !secret) throw new Error("authorized credential field is unavailable");
    const result = await this.mutate("fill", config, secret);
    this.session(config.sessionId).secrets.add(secret);
    return result;
  }

  async solveSynthetic(config) {
    const session = this.session(config.sessionId);
    const challenge = this.inventory.challenges[config.challengeId];
    if (!challenge || challenge.targetId !== session.targetId) throw new Error("synthetic challenge is unavailable for this target");
    if (session.target.mode !== "owned-assessment") throw new Error("synthetic challenges are restricted to platform-owned targets");
    if (config.attestation !== "authorized-platform-test") throw new Error("authorized platform-test attestation is required");
    if (typeof config.idempotencyKey !== "string" || config.idempotencyKey.length < 8) throw new Error("idempotencyKey is required");
    const receiptKey = `synthetic:${config.idempotencyKey}`;
    const fingerprint = digest(config.challengeId, config.target, config.attestation);
    if (session.receipts.has(receiptKey)) {
      if (session.receipts.get(receiptKey) !== fingerprint) throw new Error("idempotency key was reused with different arguments");
      return { sessionId: session.id, challengeId: config.challengeId, kind: "synthetic", solved: true, duplicate: true, receipt: config.idempotencyKey };
    }
    const engineRef = session.refs.get(config.target);
    if (!engineRef || session.refNames.get(config.target) !== challenge.accessibleName) throw new Error("synthetic challenge target is stale or does not match configured fixture");
    await session.page.click(engineRef);
    session.receipts.set(receiptKey, fingerprint);
    session.refs.clear(); session.refNames.clear(); session.mutations += 1;
    return { sessionId: session.id, challengeId: config.challengeId, kind: "synthetic", solved: true, duplicate: false, receipt: config.idempotencyKey };
  }

  report(config) {
    const session = this.session(config.sessionId);
    let outcome = "accepted";
    if ([401, 403, 429].includes(session.statusCode)) outcome = "blocked";
    else if (session.challenges.length) outcome = "challenged";
    return { sessionId: session.id, targetId: session.targetId, profileId: session.profileId, proxyPoolId: session.proxyPoolId, outcome, httpStatus: session.statusCode, challenges: session.challenges, mutationCount: session.mutations, startedAt: session.startedAt.toISOString(), evidence: { snapshotDigest: digest(session.currentURL, session.snapshotText) } };
  }

  async close(config) {
    const session = this.session(config.sessionId);
    await session.browser.close();
    this.sessions.delete(session.id);
    return { sessionId: session.id, closed: true, profilePreserved: true };
  }
}

export const test = { exactPath, detectChallenges, digest };
