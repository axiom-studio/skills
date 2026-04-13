#!/usr/bin/env bash
# Helper functions for validation scripts

log_info() { echo "[INFO] $*"; }
log_error() { echo "[ERROR] $*" >&2; }
log_warn() { echo "[WARN] $*" >&2; }
log_pass() { echo "[PASS] $*"; }
log_fail() { echo "[FAIL] $*"; }
