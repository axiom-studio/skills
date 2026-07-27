import grpc from "@grpc/grpc-js";
import protoLoader from "@grpc/proto-loader";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const definition = protoLoader.loadSync(path.join(here, "skill.proto"), { keepCase: true, defaults: true });
const protocol = grpc.loadPackageDefinition(definition).axiom.skill.v1;
const client = new protocol.SkillService(`127.0.0.1:${process.env.SKILL_PORT || "50113"}`, grpc.credentials.createInsecure());
client.Health({}, { deadline: Date.now() + 3000 }, (error, response) => {
  client.close();
  process.exit(error || !response?.healthy ? 1 : 0);
});
