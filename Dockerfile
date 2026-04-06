# Multi-stage build for aimux MCP server
# Usage: docker build -t aimux . && docker run -i aimux

# Stage 1: Build static binary
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /aimux ./cmd/aimux/

# Stage 2: Minimal runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates

COPY --from=builder /aimux /usr/local/bin/aimux
COPY --from=builder /src/config /etc/aimux/config

ENV AIMUX_CONFIG_DIR=/etc/aimux/config

ENTRYPOINT ["aimux"]
