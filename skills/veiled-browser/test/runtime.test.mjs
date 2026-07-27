import assert from "node:assert/strict";
import { mkdtemp } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { VeiledBrowserRuntime, loadInventory, test as helpers } from "../runtime.mjs";

function inventory() {
  return {
    targets: {
      owned: { baseUrl: "http://fixture.internal:8080", pathPrefixes: ["/assessment"], mode: "owned-assessment", allowPrivateSubresources: true },
      forum: { baseUrl: "https://forum.example", pathPrefixes: ["/community"], mode: "permitted-automation" },
    },
    profiles: {
      standard: {},
      varied: { preset: "windows-chrome", assessmentOnly: true },
    },
    proxyPools: {
      direct: {},
      rotating: { url: "http://proxy.internal:8080", assessmentOnly: true },
    },
    challenges: {
      checkbox: { targetId: "owned", kind: "synthetic", accessibleName: "Authorized test challenge" },
    },
  };
}

function fakeFactory(state = {}) {
  return async (options) => {
    state.launch = options;
    const page = {
      async goto(url) { state.url = url; return { url, status: state.status ?? 200, ok: true }; },
      async snapshot() {
        return state.solved
          ? { url: state.url, title: "Accepted", text: "Assessment accepted", elements: [] }
          : { url: state.url, title: "Challenge", text: state.text ?? "Synthetic CAPTCHA: Authorized test challenge", elements: [{ ref: 7, role: state.elementRole ?? "checkbox", name: state.elementName ?? "Authorized test challenge", center: { x: 10, y: 10 } }] };
      },
      async click(ref) { state.click = ref; state.solved = true; },
      async fill(ref, value) { state.fill = { ref, value }; if (state.reflectFill) state.text = `Echo ${value}`; },
    };
    return { async newPage() { return page; }, async close() { state.closed = true; } };
  };
}

async function runtime(state = {}) {
  return new VeiledBrowserRuntime({ inventory: inventory(), workspace: await mkdtemp(path.join(os.tmpdir(), "veiled-browser-")), browserFactory: fakeFactory(state), now: () => new Date("2026-07-27T00:00:00Z") });
}

test("inventory validation fails closed", () => {
  const base = {
    VEILED_BROWSER_TARGETS: JSON.stringify({ fixture: { baseUrl: "http://fixture.internal", pathPrefixes: ["/"], mode: "owned-assessment" } }),
    VEILED_BROWSER_PROFILES: JSON.stringify({ standard: {} }),
    VEILED_BROWSER_PROXY_POOLS: JSON.stringify({ direct: {} }),
  };
  assert.equal(Object.keys(loadInventory(base).targets).length, 1);
  for (const env of [
    {},
    { ...base, VEILED_BROWSER_TARGETS: JSON.stringify({ bad: { baseUrl: "file:///etc/passwd", pathPrefixes: ["/"], mode: "owned-assessment" } }) },
    { ...base, VEILED_BROWSER_TARGETS: JSON.stringify({ bad: { baseUrl: "https://example.com", pathPrefixes: ["/"], mode: "unknown" } }) },
    { ...base, VEILED_BROWSER_CHALLENGES: JSON.stringify({ bad: { targetId: "fixture", kind: "captcha-solver", accessibleName: "captcha" } }) },
  ]) assert.throws(() => loadInventory(env));
});

test("target scope cannot escape configured origin or path", () => {
  const target = inventory().targets.owned;
  assert.equal(helpers.exactPath(target, "/assessment/start"), "http://fixture.internal:8080/assessment/start");
  for (const value of ["https://other.example/assessment", "//other.example/assessment", "/admin", "relative"]) assert.throws(() => helpers.exactPath(target, value));
});

test("owned assessment uses configured identity and solves only its synthetic fixture", async () => {
  const state = {};
  const service = await runtime(state);
  await service.execute("veiled-browser-start", { sessionId: "owned-1", targetId: "owned", path: "/assessment/start", profileId: "varied", proxyPoolId: "rotating" });
  assert.equal(state.launch.fingerprint.platform, "Win32");
  assert.equal(state.launch.proxy, "http://proxy.internal:8080");
  const snapshot = await service.execute("veiled-browser-snapshot", { sessionId: "owned-1" });
  assert.deepEqual(snapshot.challenges, ["captcha", "synthetic"]);
  const solved = await service.execute("veiled-browser-solve-synthetic", { sessionId: "owned-1", challengeId: "checkbox", target: snapshot.elements[0].ref, attestation: "authorized-platform-test" });
  assert.equal(solved.solved, true);
  await service.execute("veiled-browser-snapshot", { sessionId: "owned-1" });
  assert.equal((await service.execute("veiled-browser-report", { sessionId: "owned-1" })).outcome, "accepted");
});

test("third-party automation cannot select assessment identity, egress, or synthetic solver", async () => {
  const service = await runtime({ text: "Community" });
  await assert.rejects(service.execute("veiled-browser-start", { sessionId: "bad-profile", targetId: "forum", path: "/community", profileId: "varied", proxyPoolId: "direct" }), /assessment-only/);
  await assert.rejects(service.execute("veiled-browser-start", { sessionId: "bad-proxy", targetId: "forum", path: "/community", profileId: "standard", proxyPoolId: "rotating" }), /assessment-only/);
  await service.execute("veiled-browser-start", { sessionId: "forum-1", targetId: "forum", path: "/community", profileId: "standard", proxyPoolId: "direct" });
  assert.equal((await service.execute("veiled-browser-health")).status, "ready");
  await assert.rejects(service.execute("veiled-browser-solve-synthetic", { sessionId: "forum-1", challengeId: "checkbox", target: "s1:e1", attestation: "authorized-platform-test" }), /unavailable/);
});

test("third-party challenge creates human checkpoint and blocks interaction", async () => {
  const state = { text: "Security check: verify you are human with CAPTCHA" };
  const service = await runtime(state);
  await service.execute("veiled-browser-start", { sessionId: "forum-2", targetId: "forum", path: "/community", profileId: "standard", proxyPoolId: "direct" });
  const snapshot = await service.execute("veiled-browser-snapshot", { sessionId: "forum-2" });
  assert.equal(snapshot.requiresHuman, true);
  await assert.rejects(service.execute("veiled-browser-click", { sessionId: "forum-2", target: snapshot.elements[0].ref, writeAuthorized: true, idempotencyKey: "challenge-click-1" }), /human completion/);
  assert.equal((await service.execute("veiled-browser-report", { sessionId: "forum-2" })).outcome, "challenged");
});

test("credential values stay out of outputs, errors, snapshots, and idempotent receipts", async () => {
  const state = { text: "Login form", elementName: "Password", elementRole: "textbox", reflectFill: true };
  const service = await runtime(state);
  await service.execute("veiled-browser-start", { sessionId: "login-1", targetId: "forum", path: "/community/login", profileId: "standard", proxyPoolId: "direct" });
  const snapshot = await service.execute("veiled-browser-snapshot", { sessionId: "login-1" });
  const request = { sessionId: "login-1", target: snapshot.elements[0].ref, credentialField: "password", writeAuthorized: true, idempotencyKey: "secret-fill-1" };
  await assert.rejects(service.execute("veiled-browser-fill", { ...request, value: "literal-secret" }), /credential binding/);
  const first = await service.execute("veiled-browser-fill-secret", request, { password: "correct horse battery staple" });
  assert.equal(JSON.stringify(first).includes("correct horse"), false);
  // The reference is invalidated after a mutation; duplicate replay remains bounded by the receipt.
  const duplicate = await service.execute("veiled-browser-fill-secret", request, { password: "correct horse battery staple" });
  assert.equal(duplicate.duplicate, true);
  const redacted = await service.execute("veiled-browser-snapshot", { sessionId: "login-1" });
  assert.equal(redacted.text, "Echo [REDACTED]");
  assert.equal(JSON.stringify(await service.execute("veiled-browser-report", { sessionId: "login-1" })).includes("correct horse"), false);
});
