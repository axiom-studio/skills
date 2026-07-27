import grpc from "@grpc/grpc-js";
import protoLoader from "@grpc/proto-loader";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { VeiledBrowserRuntime, loadInventory } from "./runtime.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const definition = protoLoader.loadSync(path.join(here, "skill.proto"), { keepCase: true, longs: String, enums: String, defaults: true, oneofs: true });
const protocol = grpc.loadPackageDefinition(definition).axiom.skill.v1;
const skillId = "skill-veiled-browser";
const version = "1.0.0";

function decodeMap(values = {}) {
  const decoded = {};
  for (const [key, bytes] of Object.entries(values)) {
    try { decoded[key] = JSON.parse(Buffer.from(bytes).toString("utf8")); } catch { /* invalid entries stay absent, matching the canonical server */ }
  }
  return decoded;
}
function encodeMap(values = {}) { return Object.fromEntries(Object.entries(values).map(([key, value]) => [key, Buffer.from(JSON.stringify(value))])); }
function nodeSchema(nodeType) { return Buffer.from(JSON.stringify({ nodeType, name: nodeType, displayName: nodeType, description: "Governed VeilBrowser operation", category: "security", icon: "shield", sections: [] }, null, 2)); }

export function createService(runtime) {
  return {
    async Execute(call, callback) {
      const bindings = decodeMap(call.request.bindings);
      try {
        const output = await runtime.execute(call.request.node_type, decodeMap(call.request.config), bindings);
        callback(null, { output: encodeMap(output), next_step: "" });
      } catch (error) {
        let message = error instanceof Error ? error.message : "Veiled Browser execution failed";
        for (const value of Object.values(bindings)) if (typeof value === "string" && value.length >= 3) message = message.replaceAll(value, "[REDACTED]");
        callback(null, { output: {}, error: { message, type: "execution", details: {} }, next_step: "" });
      }
    },
    GetNodeTypes(_call, callback) { callback(null, { node_types: ["veiled-browser-health", "veiled-browser-start", "veiled-browser-snapshot", "veiled-browser-click", "veiled-browser-commit", "veiled-browser-fill", "veiled-browser-fill-secret", "veiled-browser-solve-synthetic", "veiled-browser-report", "veiled-browser-close"] }); },
    GetNodeSchema(call, callback) { callback(null, { schema: nodeSchema(call.request.node_type) }); },
    Health(_call, callback) { callback(null, { healthy: true, skill_id: skillId, version }); },
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const inventory = loadInventory(process.env);
  const runtime = new VeiledBrowserRuntime({ inventory, workspace: process.env.VEILED_BROWSER_WORKSPACE || "/var/lib/openseal-veiled-browser" });
  const server = new grpc.Server();
  server.addService(protocol.SkillService.service, createService(runtime));
  const port = process.env.SKILL_PORT || "50113";
  server.bindAsync(`0.0.0.0:${port}`, grpc.ServerCredentials.createInsecure(), (error) => {
    if (error) { console.error(error.message); process.exit(1); }
  });
  const shutdown = () => server.tryShutdown(() => process.exit(0));
  process.on("SIGTERM", shutdown); process.on("SIGINT", shutdown);
}
