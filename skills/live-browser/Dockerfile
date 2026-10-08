FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgtk-3-0 libdbus-glib-1-2 libasound2 libx11-xcb1 libxt6 fonts-liberation \
    pulseaudio pulseaudio-utils ffmpeg xvfb xdotool openbox x11-utils x11vnc ca-certificates tini \
    && rm -rf /var/lib/apt/lists/*

# The shared library is installed from its relative file: path, so it must sit
# next to the skill exactly as in the repository (skills/_lib/live-browser).
ARG SKILL_NAME=live-browser
WORKDIR /opt/skill/${SKILL_NAME}
COPY skills/_lib/live-browser/package.json skills/_lib/live-browser/*.mjs skills/_lib/live-browser/*.proto /opt/skill/_lib/live-browser/
RUN rm -f /opt/skill/_lib/live-browser/*.test.mjs
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json skills/${SKILL_NAME}/.npmrc ./
RUN apt-get update && apt-get install -y --no-install-recommends make g++ \
    && npm ci --omit=dev --include=optional \
    && IMPIT_VERBOSE=1 node -e "require('impit')" \
    && apt-get purge -y make g++ && rm -rf /var/lib/apt/lists/*
ENV CAMOUFOX_INSTALL_DIR=/opt/camoufox
RUN IMPIT_VERBOSE=1 npx camoufox-js fetch && chmod -R a+rX /opt/camoufox
RUN node --input-type=module -e "import {readFileSync} from 'node:fs'; const v=JSON.parse(readFileSync('/opt/camoufox/version.json')); if(v.version!=='156.0.1'||v.release!=='beta.36') throw new Error('Camoufox build changed; review and retest before updating the release pin');"
COPY skills/${SKILL_NAME}/*.mjs skills/${SKILL_NAME}/*.proto skills/${SKILL_NAME}/entrypoint.sh ./
RUN rm -f ./*.test.mjs && chmod 755 entrypoint.sh && chown -R node:node /opt/skill

# The tenant's browser profile lives on the Skill's persistent volume
# (requirements.storage in skill.yaml), mounted at /var/lib/axiom-live-browser
# and group-writable for node (gid 1000). The directory here only backs runs
# without the volume.
RUN mkdir -p /var/lib/axiom-live-browser && chown node:node /var/lib/axiom-live-browser && chmod 700 /var/lib/axiom-live-browser
USER node
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
EXPOSE 50051
# tini is PID 1: it forwards SIGTERM to the runtime (which closes Camoufox
# cleanly) and reaps orphaned browser processes.
ENTRYPOINT ["/usr/bin/tini", "--", "/opt/skill/live-browser/entrypoint.sh"]
