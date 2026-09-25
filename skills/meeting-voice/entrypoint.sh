#!/bin/sh
set -eu

pulseaudio --start --exit-idle-time=-1
pactl load-module module-null-sink sink_name=axiom_meet_capture sink_properties=device.description=AxiomMeetCapture >/dev/null
pactl load-module module-null-sink sink_name=axiom_bot_microphone sink_properties=device.description=AxiomBotMicrophone >/dev/null
pactl load-module module-remap-source master=axiom_bot_microphone.monitor source_name=axiom_bot_source source_properties=device.description=AxiomBotSource >/dev/null
pactl set-default-sink axiom_meet_capture
pactl set-default-source axiom_bot_source
export PULSE_SINK=axiom_meet_capture
export PULSE_SOURCE=axiom_bot_source

if [ "${MEET_AUDIO_SMOKE:-}" = "1" ]; then
  exec node /app/audio-smoke.mjs
fi

if [ "${MEET_BROWSER_SMOKE:-}" = "1" ]; then
  exec node /app/browser-smoke.mjs
fi

profile_dir=${GOOGLE_PROFILES_DIR:-/profile}
mkdir -p "$profile_dir"
exec flock -n -F "$profile_dir/.meet-voice.lock" node /app/server.mjs
