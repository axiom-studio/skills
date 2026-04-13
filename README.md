# Axiom Skills Monorepo

Official Axiom Studio skills collection.

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
    ├── skill.yaml   # Skill manifest
    ├── main.go      # Implementation
    └── README.md    # Skill documentation
```

## Build Instructions

```bash
# Build all skills
make build

# Run tests
make test

# Validate skill manifests
./scripts/validate-skills.sh
```

## Contributing

1. Create a new branch from `main`
2. Add your skill under `skills/<skill-name>/`
3. Run validation: `./scripts/validate-skills.sh`
4. Submit a pull request
