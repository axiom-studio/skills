# The image is a pod of Cortex's shared browser pool: Cortex's slot server
# (chat-sandbox) runs as root under tini and starts the runtime in each slot
# as that slot's user. The slot server comes from the chat sandbox image of
# the same Cortex build: the published chat sandbox image of a Cortex release,
# pinned by digest. A local build can pass its own with --build-arg.
ARG SLOT_SERVER_IMAGE=docker.io/axiomstudio/openseal-chat-sandbox:v2.3.79@sha256:4442c1a6376a783c33df5ffb63892a28c48ec0d5559971f5d4a7d92654422ef0
FROM ${SLOT_SERVER_IMAGE} AS slot

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

# Camoufox (~2.6 GB) is copied before anything from the repository, so it is
# rebuilt only when this pin changes. `camoufox-js fetch` always downloads the
# newest build, so the pinned build is taken from the published 1.0.17 image
# by digest. Keep CAMOUFOX_JS_VERSION equal to camoufox-js in
# package-lock.json; the check after npm ci enforces it.
ARG CAMOUFOX_JS_VERSION=0.12.0
ENV CAMOUFOX_INSTALL_DIR=/opt/camoufox
# The browser always presents as Linux (os: 'linux' in camoufox-browser.mjs),
# so the macOS- and Windows-only font sets and their fontconfig are dropped
# (about 1.2 GB), and the GeoIP database too (geoip is off). Same layer as the
# copy, so the removed files never reach the image. The published build ships
# some files writable by everyone (addons/UBO); slot users of other tenants
# share these files, so nobody but root may write them.
RUN --mount=type=bind,from=axiomstudio/skill-live-browser:1.0.17@sha256:626e1bce0eeaaf6215134ba39999f21f5ce13e617efc8d2e4504b766346519a2,source=/opt/camoufox,target=/mnt/camoufox \
    cp -R --preserve=mode,timestamps /mnt/camoufox /opt/camoufox \
    && node --input-type=module -e "import {readFileSync} from 'node:fs'; const v=JSON.parse(readFileSync('/opt/camoufox/version.json')); const g=JSON.parse(readFileSync('/opt/camoufox/fonts/groups.json')); if(v.version!=='156.0.1'||v.release!=='beta.36') throw new Error('Camoufox build changed; review and retest before updating the release pin'); if(g.readBy.lin.join()!=='L,LM,LMW,LW') throw new Error('Camoufox Linux font groups changed');" \
    && rm -rf /opt/camoufox/fonts/M /opt/camoufox/fonts/MW /opt/camoufox/fonts/W \
        /opt/camoufox/fontconfig/macos /opt/camoufox/fontconfig/windows /opt/camoufox/GeoLite2-City.mmdb \
    && chmod -R go-w /opt/camoufox

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
    && apt-get purge -y --auto-remove make g++ \
    && rm -rf /var/lib/apt/lists/* /root/.npm /root/.cache /root/.node-gyp /tmp/* \
        node_modules/better-sqlite3/deps node_modules/better-sqlite3/src \
    && find node_modules/better-sqlite3/prebuilds -type f ! -name 'linux-x64.node' -delete
# install-links=true copied the library into node_modules at npm ci time; add
# its sources there (what runtime resolves) and to the repository-shaped path.
COPY skills/_lib/live-browser/*.mjs skills/_lib/live-browser/*.proto /opt/skill/_lib/live-browser/
COPY skills/_lib/live-browser/*.mjs skills/_lib/live-browser/*.proto ./node_modules/@axiom/live-browser/
RUN rm -f /opt/skill/_lib/live-browser/*.test.mjs ./node_modules/@axiom/live-browser/*.test.mjs
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json skills/${SKILL_NAME}/*.mjs skills/${SKILL_NAME}/*.proto skills/${SKILL_NAME}/entrypoint.sh ./
# Slot users (uid 20000+n) read and run everything under /opt/skill and
# /opt/camoufox; only root writes them. The runtime keeps its profile and all
# state in the slot's /tmp. Checked rather than chmod -R, which would copy
# node_modules into another layer. Symbolic links are skipped: their own
# mode is always 0777 and means nothing, and their targets are checked.
RUN rm -f ./*.test.mjs && chmod 755 entrypoint.sh \
    && bad=$(find /opt/skill /opt/camoufox ! -type l \( ! -perm -o+r -o -perm /022 -o \( -type d ! -perm -o+x \) -o ! -user root \) -print -quit) \
    && if [ -n "$bad" ]; then echo "not readable by slot users or writable by others: $bad" >&2; exit 1; fi
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

COPY --from=slot /usr/local/bin/chat-sandbox /usr/local/bin/chat-sandbox
RUN mkdir -p /workspace
WORKDIR /workspace
EXPOSE 8080
# The runtime the slot server starts in each slot: what release checks run to
# verify the Skill's Health (scripts/lib/health.sh), as a slot user would.
LABEL ai.axiom.skill.runtime.command=/opt/skill/live-browser/entrypoint.sh \
      ai.axiom.skill.runtime.port=50051 \
      ai.axiom.skill.runtime.user=20000
# tini is PID 1 and reaps the slots' holders; each holder is PID 1 of its
# slot and reaps orphaned browser processes there.
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/chat-sandbox"]
