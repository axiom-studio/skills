#!/bin/sh
set -eu

# Runs inside a slot of the shared browser pool as the slot's user, started by
# the slot server. Everything the runtime writes stays in the slot's /tmp,
# which a reset wipes.
export HOME=/tmp/home
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/run}"
mkdir -p "$HOME"
mkdir -p -m 0700 "$XDG_RUNTIME_DIR"
chmod 0700 "$XDG_RUNTIME_DIR"

# Private, per-slot PulseAudio. Each live browser gets its own null sinks
# (created by the runtime), so pages never hear or speak into each other.
pulseaudio --start --exit-idle-time=-1
exec node /opt/skill/live-browser/server.mjs
