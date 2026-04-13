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

# Build the skill binary
RUN CGO_ENABLED=0 GOOS=linux go build -o skill-database .

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/skill-database /app/skill-database

# Copy skill manifest
COPY skill.yaml /app/skill.yaml

# Expose gRPC port
EXPOSE 50052

# Set environment
ENV SKILL_PORT=50052

# Run the skill
ENTRYPOINT ["/app/skill-database"]