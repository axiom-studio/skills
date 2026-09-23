FROM python:3.12-slim

ARG SKILL_NAME=camoufox
ARG SKILL_PORT=50051
ARG TARGETARCH
ENV PYTHONUNBUFFERED=1 \
    XDG_CACHE_HOME=/opt/camoufox-cache \
    CAMOUFOX_WORKSPACE=/var/lib/openseal-camoufox \
    SKILL_PORT=${SKILL_PORT}

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        curl \
        fonts-liberation \
        libasound2 \
        libdbus-glib-1-2 \
        libgtk-3-0 \
        libx11-xcb1 \
        libxt6 \
        tini \
        xvfb \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/skill
COPY skills/${SKILL_NAME}/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY skills/${SKILL_NAME}/skill.proto ./
RUN python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. skill.proto \
    && python -m camoufox fetch \
    && chmod -R a+rX /usr/local/lib/python3.12/site-packages/camoufox \
    && useradd -r -m -u 1000 skill \
    && mkdir -p /var/lib/openseal-camoufox /opt/camoufox-cache \
    && chown -R skill:skill /var/lib/openseal-camoufox /opt/camoufox-cache /opt/skill

RUN case "${TARGETARCH:-amd64}" in \
      amd64) binary=lightpanda-x86_64-linux; checksum=1d40801e72c0bc61b2cbd3f3562bcfc46de7b79e0568f33f686b64f2e587610a ;; \
      arm64) binary=lightpanda-aarch64-linux; checksum=664775c7f5ab69cc3189954c7f9345e25c167cb4dace016173e629f9a5e82c42 ;; \
      *) echo "unsupported Lightpanda architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && curl -fsSL --retry 3 "https://github.com/lightpanda-io/browser/releases/download/0.4.1/${binary}" -o /usr/local/bin/lightpanda \
    && printf '%s  %s\n' "$checksum" /usr/local/bin/lightpanda | sha256sum -c - \
    && chmod 755 /usr/local/bin/lightpanda

# Keep frequently edited Skill code above the heavyweight, deterministic
# browser-install layer so revisions do not re-upload the browser runtime.
COPY --chown=skill:skill skills/${SKILL_NAME}/ ./

USER skill

EXPOSE ${SKILL_PORT}
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["python", "server.py"]
