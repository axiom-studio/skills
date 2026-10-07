#!/bin/sh
set -eu

# Private, per-container PulseAudio. Each live browser gets its own null sinks
# (created by the runtime), so pages never hear or speak into each other.
pulseaudio --start --exit-idle-time=-1
exec node /opt/skill/live-browser/server.mjs
