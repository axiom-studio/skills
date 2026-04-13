# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

# Copy go mod files
COPY go.mod go.sum* ./

# Download dependencies
RUN go mod download || true

# Copy source
COPY . .

# Download dependencies if not cached
RUN go mod tidy

# Build the skill binary for multiple platforms
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o skill-discord-linux-amd64 . && \
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o skill-discord-linux-arm64 .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /app/skill-discord-linux-amd64 /app/skill-discord-linux-amd64
COPY --from=builder /app/skill-discord-linux-arm64 /app/skill-discord-linux-arm64

# Copy skill manifest
COPY skill.yaml /app/skill.yaml

# Expose gRPC port
EXPOSE 50053

# Set environment
ENV SKILL_PORT=50053

# Run the skill (default to amd64, can be overridden)
ENTRYPOINT ["/app/skill-discord-linux-amd64"]
