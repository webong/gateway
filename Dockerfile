# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git build-base

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the gateway executable. Composer's vendor directory is excluded from
# the Docker context and must not be interpreted as Go's vendor directory.
RUN CGO_ENABLED=1 GOOS=linux go build -mod=mod -o gateway ./src/spinner/cmd/proxy

# Final stage
FROM alpine:3.22

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates \
    && addgroup -S gateway \
    && adduser -S -G gateway -h /app gateway

WORKDIR /app

# Copy the binary from builder
COPY --from=builder --chown=gateway:gateway /app/gateway /app/gateway

USER gateway

# HTTP ingress plus the unprivileged authoritative DNS ports used by the
# production compose example. Publish both DNS transports on public port 53.
EXPOSE 5001/tcp 2525/tcp 5353/tcp 5353/udp

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:5001/health >/dev/null || exit 1

ENTRYPOINT ["/app/gateway"]
