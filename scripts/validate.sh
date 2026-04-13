#!/usr/bin/env bash
# Axiom Skills Monorepo - Integration Test Suite
# Validates all skills build, manifests are valid, and gRPC services start

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MONOREPO_DIR="$(dirname "$SCRIPT_DIR")"
SKILLS_DIR="$MONOREPO_DIR/skills"

# Source helper libraries
source "$SCRIPT_DIR/lib/helpers.sh"
source "$SCRIPT_DIR/lib/build.sh"
source "$SCRIPT_DIR/lib/manifest.sh"
source "$SCRIPT_DIR/lib/health.sh"

# Counters
TOTAL=0
PASSED=0
FAILED=0
SKIPPED=0

log_info "========================================="
log_info "Axiom Skills Integration Test Suite"
log_info "========================================="
log_info "Monorepo: $MONOREPO_DIR"
log_info "Skills dir: $SKILLS_DIR"
log_info ""

# Phase 1: Build Validation
log_info "--- Phase 1: Build Validation ---"
run_build_validation

# Phase 2: Manifest Validation
log_info "--- Phase 2: Manifest Validation ---"
run_manifest_validation

# Phase 3: Health Check (gRPC)
log_info "--- Phase 3: Health Check ---"
run_health_checks

# Summary
log_info "========================================="
log_info "SUMMARY"
log_info "========================================="
log_info "Total: $TOTAL"
log_info "Passed: $PASSED"
log_info "Failed: $FAILED"
log_info "Skipped: $SKIPPED"

if [ "$FAILED" -gt 0 ]; then
  log_error "FAILURES: $FAILED"
  exit 1
fi

log_info "FAILURES: 0"
log_info "All tests passed!"
exit 0
