#!/usr/bin/env bash
# Run only in the isolated candidate checkout, without repository write access.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

for dependency_tool in go node npm python3 make docker; do
  command -v "$dependency_tool" >/dev/null || {
    printf 'Dependency candidate held: required tool %s is missing.\n' "$dependency_tool" >&2
    exit 1
  }
done
docker info >/dev/null

export CI=true
export GOMAXPROCS="${GOMAXPROCS:-2}"
export GOFLAGS="${GOFLAGS:+${GOFLAGS} }-p=2"

# Publication refusal and cleanup regressions are mandatory and use local
# fixtures only; they do not build, publish, or deploy actual images.
bash scripts/tests/test-release-health.sh

# Install the exact checked-in npm/Python dependency inputs before invoking the
# existing validator. All lifecycle code runs without repository write access.
npm --prefix skills/_lib/live-browser ci --no-audit --no-fund
npm --prefix skills/live-browser ci --no-audit --no-fund
dependency_gate_tmp="$(mktemp -d)"
trap 'rm -rf "$dependency_gate_tmp"' EXIT
python3 -m venv "$dependency_gate_tmp/python"
export PATH="$dependency_gate_tmp/python/bin:$PATH"
python3 -m pip install --disable-pip-version-check \
  -r skills/lightpanda/requirements.txt

go test -mod=vendor -timeout=15m ./...
# The platform module has local binding/authorization contract tests and
# explicitly opt-in external-service tests. Do not enable live provider calls.
(cd tests/platform && go test -mod=readonly -race -timeout=15m ./...)

# Build every supported Skill image with its real Dockerfile. The repository
# Makefile propagates any image failure; no registry publishing takes place.
make docker-build
# Real Health validation needs the images built above on a fresh CI runner.
bash scripts/validate.sh
