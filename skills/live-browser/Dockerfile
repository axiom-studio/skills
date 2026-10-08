# Release bumps only change the "version" fields; blank them so the npm layer
# below stays cached across releases. The real manifests are restored later.
FROM node:22-bookworm-slim AS manifests
ARG SKILL_NAME=live-browser
WORKDIR /manifests
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json skills/${SKILL_NAME}/.npmrc ./
RUN node -e "const fs=require('fs'); for (const f of ['package.json','package-lock.json']) { const j=JSON.parse(fs.readFileSync(f)); j.version='0.0.0'; if (j.packages && j.packages['']) j.packages[''].version='0.0.0'; fs.writeFileSync(f, JSON.stringify(j, null, 2)+'\n'); }"

FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgtk-3-0 libdbus-glib-1-2 libasound2 libx11-xcb1 libxt6 fonts-liberation \
    pulseaudio pulseaudio-utils ffmpeg xvfb xdotool openbox x11-utils x11vnc ca-certificates tini \
    && rm -rf /var/lib/apt/lists/*

# Camoufox (~2.6 GB) is fetched before anything from the repository is copied,
# so it is rebuilt only when this pin changes. Keep CAMOUFOX_JS_VERSION equal to
# camoufox-js in package-lock.json; the check after npm ci enforces it.
ARG CAMOUFOX_JS_VERSION=0.12.0
ENV CAMOUFOX_INSTALL_DIR=/opt/camoufox
RUN IMPIT_VERBOSE=1 npx -y camoufox-js@${CAMOUFOX_JS_VERSION} fetch && chmod -R a+rX /opt/camoufox && rm -rf /root/.npm
RUN node --input-type=module -e "import {readFileSync} from 'node:fs'; const v=JSON.parse(readFileSync('/opt/camoufox/version.json')); if(v.version!=='156.0.1'||v.release!=='beta.36') throw new Error('Camoufox build changed; review and retest before updating the release pin');"

# The shared library is installed from its relative file: path, so it must sit
# next to the skill exactly as in the repository (skills/_lib/live-browser).
# Only manifests go in before npm ci, so source edits keep that layer cached.
ARG SKILL_NAME=live-browser
WORKDIR /opt/skill/${SKILL_NAME}
COPY skills/_lib/live-browser/package.json /opt/skill/_lib/live-browser/
COPY --from=manifests /manifests/ ./
RUN apt-get update && apt-get install -y --no-install-recommends make g++ \
    && npm ci --omit=dev --include=optional \
    && IMPIT_VERBOSE=1 node -e "require('impit')" \
    && node -e "const v=require('camoufox-js/package.json').version; if (v!=='${CAMOUFOX_JS_VERSION}') throw new Error('camoufox-js '+v+' is locked; update CAMOUFOX_JS_VERSION to match')" \
    && apt-get purge -y make g++ && rm -rf /var/lib/apt/lists/*
# install-links=true copied the library into node_modules at npm ci time; add
# its sources there (what runtime resolves) and to the repository-shaped path.
COPY skills/_lib/live-browser/*.mjs skills/_lib/live-browser/*.proto /opt/skill/_lib/live-browser/
COPY skills/_lib/live-browser/*.mjs skills/_lib/live-browser/*.proto ./node_modules/@axiom/live-browser/
RUN rm -f /opt/skill/_lib/live-browser/*.test.mjs ./node_modules/@axiom/live-browser/*.test.mjs
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json skills/${SKILL_NAME}/*.mjs skills/${SKILL_NAME}/*.proto skills/${SKILL_NAME}/entrypoint.sh ./
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
