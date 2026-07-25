FROM golang:1.26-bookworm AS builder
WORKDIR /src

COPY go.mod go.sum ./
COPY vendor ./vendor
COPY skills/browser ./skills/browser
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -mod=vendor -trimpath -buildvcs=false -ldflags="-s -w" \
    -o /out/skill ./skills/browser

# Playwright's pinned image supplies Chromium and its runtime libraries. The
# skill uses agent-browser directly; Playwright is not part of the public API.
FROM mcr.microsoft.com/playwright:v1.61.0-noble

ARG AGENT_BROWSER_VERSION=0.27.2
USER root
RUN npm install --global --omit=dev "agent-browser@${AGENT_BROWSER_VERSION}" \
    && apt-get update \
    && apt-get install --yes --no-install-recommends tini \
    && rm -rf /var/lib/apt/lists/* \
    && chromium_path="$(find /ms-playwright -type f -path '*/chrome-linux*/chrome' -print -quit)" \
    && test -n "${chromium_path}" \
    && ln -s "${chromium_path}" /usr/local/bin/openseal-chromium

WORKDIR /app
COPY --from=builder /out/skill /app/skill
COPY skills/browser/skill.yaml /app/skill.yaml

RUN install -d -o pwuser -g pwuser -m 0700 /var/lib/openseal-browser

ENV BROWSER_WORKSPACE=/var/lib/openseal-browser \
    CHROMIUM_EXECUTABLE=/usr/local/bin/openseal-chromium \
    AGENT_BROWSER_BINARY=/usr/local/bin/agent-browser \
    BROWSER_IDLE_TIMEOUT=15m \
    SKILL_PORT=50112

VOLUME ["/var/lib/openseal-browser"]
EXPOSE 50112
USER pwuser

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD agent-browser --version >/dev/null && test -w "${BROWSER_WORKSPACE}" || exit 1

ENTRYPOINT ["/usr/bin/tini", "--", "/app/skill"]
