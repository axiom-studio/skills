#!/usr/bin/env bash
# Print the immutable image ID only after its runtime matches its declaration.
set -euo pipefail
if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: $0 axiomstudio/skill-<name>:<declared-tag> [skill.yaml]" >&2
  exit 2
fi
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/health.sh"
verify_skill_image "$@"
