# Axiom Skills Monorepo

Official Axiom Studio skills collection. Every published capability is a
complete canonical OpenSeal `SkillDefinition`; this repository does not publish
legacy node/executor manifests.

## Overview

This monorepo contains all Axiom Studio skills — reusable, composable capabilities that extend the platform's AI and automation features.

## Structure

```
skills-monorepo/
├── skills/          # All skills as subdirectories
├── scripts/         # Validation and build scripts
├── go.mod           # Go module definition
├── README.md        # This file
└── .gitignore       # Git ignore rules
```

## Adding a New Skill

Each skill lives in its own subdirectory under `skills/`:

```
skills/
└── my-skill/
    ├── skill.yaml   # Canonical OpenSeal SkillDefinition
    ├── main.go      # Implementation
    └── README.md    # Skill documentation
```

## Build Instructions

```bash
# Validate Go implementations and manifest structure
./scripts/validate.sh

# Validate one manifest with the OpenSeal CLI
openseal skill validate skills/<skill-name>/skill.yaml

# Build or publish the exact OCI packages declared by each definition
make docker-build
make docker-push
```

## Contributing

1. Create a new branch from `main`
2. Add your canonical Skill under `skills/<skill-name>/`
3. Run validation: `./scripts/validate.sh`
4. Submit a pull request

## Learning new API and MCP integrations

The [generic API](skills/api/SKILL.md) and [generic MCP](skills/mcp/SKILL.md) skills
let agents learn a service contract and compile it into reusable named OpenSeal
blocks. They share a provider-neutral runtime with pinned profiles, schema checks,
and separate credential bindings. Compiled binding plans use OpenSeal's existing
`upsert_binding` lifecycle to persist and activate learned contracts on the shared
runtimes. See the [contract reference](internal/integration/README.md)
for the learning/provisioning workflow, supported features, and current limitations.
