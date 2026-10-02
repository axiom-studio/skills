# Build with the canonical repository vendor tree; no private module downloads.
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -buildvcs=false -trimpath -ldflags="-s -w" -o /app/skill ./skills/mcp

FROM node:22-alpine
RUN apk add --no-cache ca-certificates python3 py3-pip uv
WORKDIR /app
COPY --from=builder /app/skill /app/skill
COPY skills/mcp/skill.yaml /app/skill.yaml
# Explicit local-process support in this tenant-isolated runtime image.
ENV SKILL_PORT=50051 INTEGRATION_STDIO_ALLOWED=1
EXPOSE 50051
ENTRYPOINT ["/app/skill"]
