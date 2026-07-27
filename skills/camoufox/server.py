"""Canonical Skill gRPC transport for the Camoufox skill."""

import json
import os
from concurrent import futures

import grpc
import yaml

import skill_pb2
import skill_pb2_grpc
from runtime import CamoufoxRuntime, load_inventory

SKILL_ID = "skill-camoufox"
VERSION = "1.0.1"
ACTIONS = [
    "camoufox-health",
    "camoufox-start",
    "camoufox-snapshot",
    "camoufox-click",
    "camoufox-commit",
    "camoufox-fill",
    "camoufox-fill-secret",
    "camoufox-select",
    "camoufox-scroll",
    "camoufox-screenshot",
    "camoufox-report",
    "camoufox-close",
]
WORKSPACE = os.environ.get("CAMOUFOX_WORKSPACE", "/var/lib/openseal-camoufox")
PORT = os.environ.get("SKILL_PORT", "50051")
MANIFEST_PATH = os.path.join(os.path.dirname(__file__), "skill.yaml")


def decode(mapping):
    values = {}
    for key, raw in (mapping or {}).items():
        text = raw.decode("utf-8") if isinstance(raw, (bytes, bytearray)) else str(raw)
        try:
            values[key] = json.loads(text)
        except (json.JSONDecodeError, ValueError):
            values[key] = text
    return values


def load_action_schemas(path=MANIFEST_PATH):
    with open(path, "r", encoding="utf-8") as stream:
        document = yaml.safe_load(stream)
    actions = document.get("definition", {}).get("actions", {})
    schemas = {}
    for action in ACTIONS:
        if action not in actions:
            raise ValueError(f"manifest is missing action {action}")
        schema = actions[action].get("inputSchema")
        if not isinstance(schema, dict):
            raise ValueError(f"manifest action {action} has no input schema")
        schemas[action] = schema
    return schemas


def redact_error(error, bindings):
    message = str(error) or error.__class__.__name__
    for value in bindings.values():
        if isinstance(value, str) and len(value) >= 3:
            message = message.replace(value, "[REDACTED]")
    return message


def encode_output(result):
    if not isinstance(result, dict):
        raise TypeError("Skill action output must be an object")
    return {key: json.dumps(value).encode("utf-8") for key, value in result.items()}


class SkillService(skill_pb2_grpc.SkillServiceServicer):
    def __init__(self):
        self.runtime = CamoufoxRuntime(inventory=load_inventory(), workspace=WORKSPACE)
        self.schemas = load_action_schemas()

    def Execute(self, request, _context):
        bindings = decode(request.bindings)
        try:
            result = self.runtime.execute(request.node_type, decode(request.config), bindings)
            return skill_pb2.ExecuteResponse(output=encode_output(result))
        except Exception as exc:
            error_type = "validation" if isinstance(exc, (TypeError, ValueError)) else "execution"
            return skill_pb2.ExecuteResponse(
                error=skill_pb2.Error(message=redact_error(exc, bindings), type=error_type)
            )

    def GetNodeTypes(self, _request, _context):
        return skill_pb2.GetNodeTypesResponse(node_types=ACTIONS)

    def GetNodeSchema(self, request, _context):
        schema = self.schemas.get(request.node_type)
        if schema is None:
            _context.set_code(grpc.StatusCode.NOT_FOUND)
            _context.set_details(f"unknown node type: {request.node_type}")
            return skill_pb2.GetNodeSchemaResponse()
        return skill_pb2.GetNodeSchemaResponse(schema=json.dumps(schema, sort_keys=True).encode("utf-8"))

    def Health(self, _request, _context):
        return skill_pb2.HealthResponse(healthy=True, skill_id=SKILL_ID, version=VERSION)


def serve():
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    skill_pb2_grpc.add_SkillServiceServicer_to_server(SkillService(), server)
    server.add_insecure_port(f"[::]:{PORT}")
    server.start()
    server.wait_for_termination()


if __name__ == "__main__":
    serve()
