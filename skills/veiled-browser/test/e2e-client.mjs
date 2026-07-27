import assert from "node:assert/strict";
import grpc from "@grpc/grpc-js";
import protoLoader from "@grpc/proto-loader";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const definition = protoLoader.loadSync(path.join(here, "..", "skill.proto"), { keepCase: true, defaults: true });
const protocol = grpc.loadPackageDefinition(definition).axiom.skill.v1;
const client = new protocol.SkillService(process.env.VEILED_BROWSER_E2E_ENDPOINT || "127.0.0.1:55113", grpc.credentials.createInsecure());

function encoded(values) { return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, Buffer.from(JSON.stringify(value))])); }
function execute(nodeType, config) {
  return new Promise((resolve, reject) => client.Execute({ node_id: `e2e-${nodeType}`, node_type: nodeType, config: encoded(config), input: {}, bindings: {} }, (error, response) => {
    if (error) return reject(error);
    if (response.error?.message) return reject(new Error(response.error.message));
    resolve(Object.fromEntries(Object.entries(response.output).map(([key, bytes]) => [key, JSON.parse(Buffer.from(bytes).toString("utf8"))])));
  }));
}

try {
  await execute("veiled-browser-start", { sessionId: "chrome-e2e", targetId: "fixture", path: "/", profileId: "windows", proxyPoolId: "direct" });
  const challenged = await execute("veiled-browser-snapshot", { sessionId: "chrome-e2e" });
  assert.deepEqual(challenged.challenges, ["synthetic"], JSON.stringify(challenged));
  const target = challenged.elements.find((element) => element.name === "Authorized test challenge");
  assert.ok(target, "real Chromium snapshot did not expose the configured challenge");
  const solved = await execute("veiled-browser-solve-synthetic", { sessionId: "chrome-e2e", challengeId: "checkbox", target: target.ref, attestation: "authorized-platform-test" });
  assert.equal(solved.solved, true);
  const accepted = await execute("veiled-browser-snapshot", { sessionId: "chrome-e2e" });
  assert.equal(accepted.text.includes("Assessment accepted"), true);
  const report = await execute("veiled-browser-report", { sessionId: "chrome-e2e" });
  assert.equal(report.outcome, "accepted");
  assert.match(report.evidence.snapshotDigest, /^sha256:[a-f0-9]{64}$/);
  await execute("veiled-browser-close", { sessionId: "chrome-e2e" });
  console.log(JSON.stringify({ success: true, engine: "veilbrowser", chromium: true, outcome: report.outcome, challenge: "synthetic" }));
} finally {
  client.close();
}
