"""Operator config for a Python Skill, with the Go Skills SDK's semantics.

A Skill declares its settings as a dataclass whose fields are built with
``setting(...)``. ``ConfigSpec`` derives the shared field schema from it
(reported over Health), decodes the effective config the platform attaches to
each call (``ExecutionContext.config``, resolved for ``ExecutionContext.tenant_id``)
into that dataclass with declared defaults, and runs the Skill's optional config
check for the Health RPC. It mirrors ``config.From``/``WithConfig``/
``WithConfigCheck`` in github.com/axiom-studio/skills.sdk/config.

This module is self-contained on purpose: it is the only Python Skill with
config today. When a second Python Skill needs config, move it unchanged into a
shared Python Skill library instead of copying it.
"""

from __future__ import annotations

import dataclasses
import json
import math
import re
import threading
from collections import OrderedDict

CONFIG_ERROR_TYPE = "config"
CACHE_SIZE = 256
SCOPES = ("global", "tenant")
TYPES = ("string", "enum", "bool", "int", "secret", "url")
AUDIENCES = ("skill", "host")
KEY_PATTERN = re.compile(r"[a-z][A-Za-z0-9]{0,63}")
CHECK_STATUSES = ("ok", "warn", "fail")
_REDACTED = "[redacted]"
_MISSING = dataclasses.MISSING


class Secret:
    """A secret config value. It never prints or serializes; call reveal()."""

    __slots__ = ("_value",)

    def __init__(self, value=None):
        object.__setattr__(self, "_value", value or None)

    def __setattr__(self, _name, _value):
        raise AttributeError("Secret is immutable")

    def reveal(self):
        return self._value or ""

    def is_set(self):
        return bool(self._value)

    def __repr__(self):
        return f"Secret({_REDACTED})"

    def __str__(self):
        return _REDACTED

    def __format__(self, _spec):
        return _REDACTED

    def __eq__(self, other):
        return isinstance(other, Secret) and other._value == self._value

    def __hash__(self):
        return hash((Secret, self._value))

    def __reduce__(self):
        raise TypeError("Secret values cannot be pickled")


class ConfigError(ValueError):
    """Invalid operator config. Messages name keys, never a secret's value."""

    type = CONFIG_ERROR_TYPE


def setting(key, *, label, type, description="", enum=None, default=_MISSING, required=False,
            required_when=None, scopes=None, audience=None, summary=False, min=None, max=None,
            pattern=None):
    """Declare one config field of a config dataclass."""
    spec = {"key": key, "label": label, "description": description, "type": type,
            "enum": tuple(enum) if enum is not None else None, "default": default,
            "required": required, "requiredWhen": dict(required_when) if required_when else None,
            "scopes": tuple(scopes) if scopes else None, "audience": audience, "summary": summary,
            "min": min, "max": max, "pattern": pattern}
    metadata = {"skill_config": spec}
    if type == "secret":
        if default is not _MISSING:
            raise ValueError(f"config: field {key}: secret fields cannot have a default")
        return dataclasses.field(default_factory=Secret, metadata=metadata)
    if default is _MISSING:
        zero = {"bool": False, "int": 0}.get(type, "")
        return dataclasses.field(default=zero, metadata=metadata)
    return dataclasses.field(default=default, metadata=metadata)


class _Field:
    def __init__(self, attr, spec, section_scopes):
        self.attr = attr
        self.key = spec["key"]
        self.label = spec["label"]
        self.description = spec["description"] or ""
        self.type = spec["type"]
        self.enum = spec["enum"]
        self.default = spec["default"]
        self.required = bool(spec["required"])
        self.required_when = spec["requiredWhen"] or {}
        self.scopes = spec["scopes"]
        self.audience = spec["audience"] or "skill"
        self.summary = bool(spec["summary"])
        self.min = spec["min"]
        self.max = spec["max"]
        self.pattern = spec["pattern"]
        self.regex = None
        self._validate(section_scopes)

    def _fail(self, message):
        raise ValueError(f"config: field {self.key}: {message}")

    def _validate(self, section_scopes):
        if not KEY_PATTERN.fullmatch(self.key or ""):
            raise ValueError(f"config: key {self.key!r} must match {KEY_PATTERN.pattern}")
        if not self.label or len(self.label) > 60 or self.label != self.label.strip():
            self._fail("label is required, trimmed, at most 60 characters")
        if len(self.description) > 200 or self.description != self.description.strip():
            self._fail("description must be trimmed, at most 200 characters")
        if self.type not in TYPES:
            self._fail(f"type {self.type!r} must be one of {', '.join(TYPES)}")
        if (self.type == "enum") != (self.enum is not None):
            self._fail("enum is required for, and only allowed on, type enum")
        if self.enum is not None and (not self.enum or len(set(self.enum)) != len(self.enum)
                                      or not all(isinstance(v, str) and v for v in self.enum)):
            self._fail("enum values must be non-empty and unique")
        if (self.min is not None or self.max is not None) and self.type != "int":
            self._fail("min and max are only allowed on type int")
        if self.min is not None and self.max is not None and self.min > self.max:
            self._fail("min exceeds max")
        if self.pattern is not None:
            if self.type != "string":
                self._fail("pattern is only allowed on type string")
            self.regex = re.compile(self.pattern)
        if self.audience not in AUDIENCES:
            self._fail(f"audience {self.audience!r} must be skill or host")
        if self.scopes is not None:
            if not self.scopes or any(s not in section_scopes for s in self.scopes):
                self._fail(f"scopes must be a subset of the section scopes {list(section_scopes)}")
            self.scopes = tuple(s for s in SCOPES if s in self.scopes)
            if self.scopes == tuple(section_scopes):
                self.scopes = None  # the default
        if self.default is not _MISSING:
            self.check(self.default)

    def check(self, value):
        """Validate a typed value against the field's type and constraints."""
        if self.type == "bool":
            ok = isinstance(value, bool)
        elif self.type == "int":
            ok = isinstance(value, int) and not isinstance(value, bool)
        else:
            ok = isinstance(value, str)
        if not ok:
            raise ConfigError(f"{self.key}: not a {self.type}")
        if self.type == "enum" and value not in self.enum:
            raise ConfigError(f"{self.key}: {value!r} is not one of {list(self.enum)}")
        if self.type == "int":
            if self.min is not None and value < self.min:
                raise ConfigError(f"{self.key}: {value} is below min {self.min}")
            if self.max is not None and value > self.max:
                raise ConfigError(f"{self.key}: {value} is above max {self.max}")
        if self.type == "url" and not re.match(r"https?://[^/\s]+", value):
            raise ConfigError(f"{self.key}: not an absolute http(s) URL")
        if self.regex is not None and not self.regex.search(value):
            raise ConfigError(f"{self.key}: value does not match pattern {self.pattern}")

    def decode(self, raw):
        """Convert a delivered JSON value; errors never echo a secret."""
        if self.type == "int" and isinstance(raw, float) and not isinstance(raw, bool):
            if not math.isfinite(raw) or raw != math.trunc(raw) or abs(raw) >= 2 ** 63:
                raise ConfigError(f"{self.key}: not a int")
            raw = int(raw)
        if self.type == "secret":
            if not isinstance(raw, str):
                raise ConfigError(f"{self.key}: not a secret")
            return Secret(raw)
        self.check(raw)
        return raw

    def is_empty(self, value):
        if isinstance(value, Secret):
            return not value.is_set()
        return value is None or value == ""

    def schema(self):
        out = {"key": self.key, "label": self.label}
        if self.description:
            out["description"] = self.description
        out["type"] = self.type
        if self.enum is not None:
            out["enum"] = list(self.enum)
        if self.default is not _MISSING:
            out["default"] = self.default
        if self.required:
            out["required"] = True
        if self.required_when:
            out["requiredWhen"] = dict(self.required_when)
        if self.scopes is not None:
            out["scopes"] = list(self.scopes)
        if self.audience != "skill":
            out["audience"] = self.audience
        if self.summary:
            out["summary"] = True
        if self.min is not None:
            out["min"] = self.min
        if self.max is not None:
            out["max"] = self.max
        if self.pattern is not None:
            out["pattern"] = self.pattern
        return out


def check_result(checks, status=None):
    """A config check result; an omitted status is the worst row's status."""
    rows = [dict(row) for row in checks]
    for row in rows:
        if row.get("status") not in CHECK_STATUSES:
            raise ValueError(f"config check row status {row.get('status')!r} must be ok, warn or fail")
    if status is None:
        statuses = {row["status"] for row in rows}
        status = "fail" if "fail" in statuses else "warn" if "warn" in statuses else "ok"
    return {"status": status, "checks": rows}


def struct_to_dict(struct):
    """Plain dict of a google.protobuf.Struct (numbers arrive as floats)."""
    if struct is None or isinstance(struct, dict):
        return struct
    from google.protobuf.json_format import MessageToDict
    return MessageToDict(struct)


def _message_field(message, name):
    """A set sub-message field, or None. Plain objects (tests) need no HasField."""
    has_field = getattr(message, "HasField", None)
    if has_field is not None:
        try:
            return getattr(message, name) if has_field(name) else None
        except ValueError:
            return None
    return getattr(message, name, None)


class ConfigSpec:
    """A Skill's declared config dataclass, its decode cache and optional check."""

    def __init__(self, cls, *, scopes=("global",), check=None):
        if not dataclasses.is_dataclass(cls):
            raise TypeError(f"config: {cls!r} is not a dataclass")
        if not scopes or any(s not in SCOPES for s in scopes):
            raise ValueError("config: scopes must be a non-empty subset of global, tenant")
        self.cls = cls
        self.scopes = tuple(s for s in SCOPES if s in scopes)
        self._check = check
        self.fields = []
        for f in dataclasses.fields(cls):
            spec = f.metadata.get("skill_config")
            if spec is None:
                raise ValueError(f"config: field {f.name} is not declared with setting()")
            self.fields.append(_Field(f.name, spec, self.scopes))
        if not self.fields:
            raise ValueError(f"config: {cls.__name__} declares no fields")
        by_key = {}
        for field in self.fields:
            if field.key in by_key:
                raise ValueError(f"config: duplicate key {field.key}")
            by_key[field.key] = field
        for field in self.fields:
            for name, value in field.required_when.items():
                ref = by_key.get(name)
                if ref is None:
                    field._fail(f"requiredWhen references unknown field {name}")
                if ref is field:
                    field._fail("requiredWhen references itself")
                if ref.type == "secret":
                    field._fail(f"requiredWhen cannot reference secret {name}")
                ref.check(value)
        self._lock = threading.Lock()
        self._cache = OrderedDict()  # (tenant, version) -> config, most recent last
        self.cache_size = CACHE_SIZE
        # Declared defaults, without required checks (an unset required
        # field fails calls, not the Skill's start).
        self.defaults = self._decode(None, check_required=False)

    @property
    def has_check(self):
        return self._check is not None

    def schema(self):
        return {"scopes": list(self.scopes), "fields": [f.schema() for f in self.fields]}

    def schema_json(self):
        return json.dumps(self.schema(), separators=(",", ":")).encode("utf-8")

    def decode(self, values):
        """Declared defaults overlaid with values. Missing and null keys keep
        their default, unknown keys are ignored, host-audience fields are never
        set, and every invalid value (including an unset required or
        requiredWhen field) is reported in one ConfigError."""
        return self._decode(values, check_required=True)

    def _decode(self, values, *, check_required):
        values = struct_to_dict(values) or {}
        if not isinstance(values, dict):
            raise ConfigError("invalid config: not an object")
        decoded, errors = {}, []
        for field in self.fields:
            if field.audience == "host":
                continue
            raw = values.get(field.key)
            if raw is None:
                continue
            try:
                decoded[field.attr] = field.decode(raw)
            except ConfigError as exc:
                errors.append(str(exc))
        cfg = self.cls(**decoded)
        if check_required and not errors:
            effective = {f.key: getattr(cfg, f.attr) for f in self.fields}
            for field in self.fields:
                if field.audience == "host" or not field.is_empty(effective[field.key]):
                    continue
                if field.required:
                    errors.append(f"{field.key}: required")
                elif field.required_when and all(effective.get(k) == v for k, v in field.required_when.items()):
                    when = ", ".join(f"{k}={v}" for k, v in field.required_when.items())
                    errors.append(f"{field.key}: required when {when}")
        if errors:
            raise ConfigError("invalid config: " + "; ".join(errors))
        return cfg

    def for_call(self, context):
        """The config of one call. The tenant comes only from
        ExecutionContext.tenant_id; decoded configs are cached by (tenant,
        config_version), and an empty version is never cached."""
        tenant = getattr(context, "tenant_id", "") or ""
        version = getattr(context, "config_version", "") or ""
        values = _message_field(context, "config") if context is not None else None
        return self.decode_call(tenant, version, values)

    def decode_call(self, tenant, version, values):
        if not version:
            return self.decode(values)
        key = (tenant, version)
        with self._lock:
            if key in self._cache:
                self._cache.move_to_end(key)
                return self._cache[key]
        cfg = self.decode(values)
        with self._lock:
            if key in self._cache:  # decoded concurrently
                self._cache.move_to_end(key)
                return self._cache[key]
            # A tenant's older versions are never asked for again.
            for stale in [k for k in self._cache if k[0] == tenant]:
                del self._cache[stale]
            self._cache[key] = cfg
            while len(self._cache) > self.cache_size:
                self._cache.popitem(last=False)
        return cfg

    def check(self, values):
        """Run the declared check against values (None: declared defaults).
        Returns None when no check is declared; a decode error is a failed check."""
        if self._check is None:
            return None
        try:
            cfg = self.decode(values)
        except ConfigError as exc:
            return check_result([{"id": "config", "label": "Config values", "status": "fail",
                                  "detail": str(exc)}], "fail")
        try:
            result = self._check(cfg)
            return check_result(result["checks"], result.get("status"))
        except Exception as exc:  # a broken check is a failed check, not a crash
            return check_result([{"id": "panic", "label": "Config check", "status": "fail",
                                  "detail": exc.__class__.__name__}], "fail")

    def health_status(self, pb, request):
        """HealthResponse.config for a HealthRequest, built with the Skill's pb2 module."""
        status = pb.ConfigStatus(schema=self.schema_json(), has_config_check=self.has_check)
        if getattr(request, "config_check", False) and self.has_check:
            result = self.check(_message_field(request, "config_values"))
            status.config_check.CopyFrom(pb.ConfigCheckResult(status=result["status"], checks=[
                pb.ConfigCheck(id=row.get("id", ""), label=row.get("label", ""), status=row["status"],
                               detail=row.get("detail", ""))
                for row in result["checks"]]))
        return status
