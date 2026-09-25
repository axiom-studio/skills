FROM node:22-bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    chromium pulseaudio pulseaudio-utils ffmpeg ca-certificates util-linux \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
ARG SKILL_NAME=meet-voice
COPY skills/${SKILL_NAME}/package.json skills/${SKILL_NAME}/package-lock.json ./
RUN npm ci --omit=dev
COPY skills/${SKILL_NAME}/*.mjs skills/${SKILL_NAME}/skill.proto skills/${SKILL_NAME}/entrypoint.sh ./
RUN chmod 755 /app/entrypoint.sh && mkdir -p /profile && chown -R node:node /app /profile

USER node
ENV CHROMIUM_PATH=/usr/bin/chromium
ENV GOOGLE_PROFILES_DIR=/profile
EXPOSE 50051
ENTRYPOINT ["/app/entrypoint.sh"]
