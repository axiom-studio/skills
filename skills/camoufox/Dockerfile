FROM python:3.12-slim

ARG SKILL_NAME=camoufox
ARG SKILL_PORT=50051
ENV PYTHONUNBUFFERED=1 \
    CAMOUFOX_WORKSPACE=/var/lib/openseal-camoufox \
    SKILL_PORT=${SKILL_PORT}

RUN apt-get update \
    && apt-get install -y --no-install-recommends xvfb \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/skill
COPY skills/${SKILL_NAME}/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY skills/${SKILL_NAME}/ ./
RUN python -m grpc_tools.protoc -I. --python_out=. --grpc_python_out=. skill.proto \
    && python -m camoufox fetch \
    && chmod -R a+rX /usr/local/lib/python3.12/site-packages/camoufox \
    && useradd -r -m -u 1000 skill \
    && mkdir -p /var/lib/openseal-camoufox \
    && chown -R skill:skill /var/lib/openseal-camoufox /opt/skill

USER skill

EXPOSE ${SKILL_PORT}
CMD ["python", "server.py"]
