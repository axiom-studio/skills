FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
COPY vendor ./vendor
COPY skills/google-workspace ./skills/google-workspace
RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -buildvcs=false -ldflags="-s -w" -o /app/skill ./skills/google-workspace

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S skill && adduser -S -G skill skill
WORKDIR /app
COPY --from=builder /app/skill /app/skill
COPY skills/google-workspace/skill.yaml /app/skill.yaml
USER skill
ENV SKILL_PORT=50051
EXPOSE 50051
ENTRYPOINT ["/app/skill"]
