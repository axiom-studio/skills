# Generic Dockerfile for building any skill in the Axiom Skills Monorepo
# Usage: docker build -f Dockerfile --build-arg SKILL_NAME=<name> --build-arg SKILL_PORT=<port> -t <image> .
ARG SKILL_NAME
ARG SKILL_PORT=50051

# Build stage
FROM golang:1.25-alpine AS builder
ARG SKILL_NAME
ARG SKILL_PORT
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -buildvcs=false \
    -o skill \
    ./skills/${SKILL_NAME}/...

# Runtime stage
FROM alpine:3.19
ARG SKILL_NAME
ARG SKILL_PORT
WORKDIR /app

COPY --from=builder /app/skill /app/skill
COPY skills/${SKILL_NAME}/skill.yaml /app/skill.yaml

EXPOSE ${SKILL_PORT}
ENV SKILL_PORT=${SKILL_PORT}

ENTRYPOINT ["/app/skill"]
