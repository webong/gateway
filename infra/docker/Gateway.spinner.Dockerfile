# Standalone Spinner host image. Automation and companion capabilities arrive
# as verified extension packages; the host compiles no concrete script runtime.
FROM golang:1.26-bookworm AS host-builder

ENV GOTELEMETRY=off

COPY . /src/gateway
WORKDIR /src/gateway/pkg/spinner/cmd/gateway
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -mod=readonly -trimpath -ldflags="-s -w" -o /tmp/gateway .

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN groupadd -g 10001 gateway \
    && useradd -u 10001 -g gateway -d /app gateway \
    && mkdir -p /data/extensions \
    && chown -R gateway:gateway /data

WORKDIR /app
COPY --from=host-builder /tmp/gateway /app/gateway

ENV GATEWAY_PLUGIN_DIR=/data/extensions
VOLUME /data

USER 10001:10001

# HTTP ingress plus the unprivileged authoritative DNS ports used by the
# production compose example. Publish both DNS transports on public port 53.
EXPOSE 5001/tcp 2525/tcp 5353/tcp 5353/udp

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -fsS http://127.0.0.1:5001/health >/dev/null || exit 1

ENTRYPOINT ["/app/gateway"]
