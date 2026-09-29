FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    libgtk-3-0 libdbus-glib-1-2 libasound2 libx11-xcb1 libxt6 fonts-liberation \
    pulseaudio pulseaudio-utils ffmpeg xvfb xdotool openbox x11-utils ca-certificates util-linux \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
ARG SKILL_NAME=meeting-voice
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json ./
RUN apt-get update && apt-get install -y --no-install-recommends make g++ \
    && npm ci --omit=dev --include=optional \
    && IMPIT_VERBOSE=1 node -e "require('impit')" \
    && apt-get purge -y make g++ && rm -rf /var/lib/apt/lists/*
ENV CAMOUFOX_INSTALL_DIR=/opt/camoufox
RUN IMPIT_VERBOSE=1 npx camoufox-js fetch && chmod -R a+rX /opt/camoufox
RUN node --input-type=module -e "import {readFileSync} from 'node:fs'; const v=JSON.parse(readFileSync('/opt/camoufox/version.json')); if(v.version!=='152.0.4'||v.release!=='beta.31') throw new Error('Camoufox build changed; review and retest before updating the release pin');"
RUN apt-get update && apt-get install -y --no-install-recommends x11vnc && rm -rf /var/lib/apt/lists/*
COPY skills/${SKILL_NAME}/*.mjs skills/${SKILL_NAME}/*.proto skills/${SKILL_NAME}/entrypoint.sh ./
RUN chmod 755 /app/entrypoint.sh && mkdir -p /profile && chown -R node:node /app /profile

USER node
ENV PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1
ENV GOOGLE_PROFILES_DIR=/profile
EXPOSE 50051
ENTRYPOINT ["/app/entrypoint.sh"]
