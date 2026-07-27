FROM node:24-bookworm-slim

ARG VEIL_BROWSER_VERSION=1.3.1
WORKDIR /app

RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates chromium chromium-sandbox fonts-liberation tini xvfb \
    && rm -rf /var/lib/apt/lists/*

COPY skills/veiled-browser/package.json skills/veiled-browser/package-lock.json ./
RUN npm ci --omit=dev --ignore-scripts \
    && test "$(node -p "require('@achamm/veilbrowser/package.json').version")" = "${VEIL_BROWSER_VERSION}"

COPY skills/veiled-browser/server.mjs skills/veiled-browser/runtime.mjs skills/veiled-browser/skill.proto skills/veiled-browser/healthcheck.mjs ./
COPY skills/veiled-browser/skill.yaml /app/skill.yaml

RUN install -d -o node -g node -m 0700 /var/lib/openseal-veiled-browser

ENV SKILL_PORT=50113 \
    VEILED_BROWSER_WORKSPACE=/var/lib/openseal-veiled-browser

VOLUME ["/var/lib/openseal-veiled-browser"]
EXPOSE 50113
USER node

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["node", "/app/healthcheck.mjs"]
ENTRYPOINT ["/usr/bin/tini", "--", "node", "/app/server.mjs"]
