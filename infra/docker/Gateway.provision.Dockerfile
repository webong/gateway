ARG GATEWAY_WORKER_ALPINE_VERSION=3.22
FROM golang:1.26-alpine AS builder

WORKDIR /src/gateway
COPY . .
RUN GOWORK=off go -C pkg/spinner mod download

# The worker and workload integrations do not embed PHP or script runtimes.
RUN CGO_ENABLED=0 GOWORK=off go -C pkg/spinner build -mod=readonly -trimpath -o /out/gateway-provision ./cmd/provision \
    && for integration in centrifugo mercure reverb; do \
        CGO_ENABLED=0 GOWORK=off go -C "pkg/$integration" build -mod=readonly -trimpath \
            -o "/out/gateway-$integration" ./cmd/integration; \
    done

FROM alpine:${GATEWAY_WORKER_ALPINE_VERSION} AS worker-base

RUN apk add --no-cache ca-certificates \
    && addgroup -g 10001 -S gateway \
    && adduser -u 10001 -S -G gateway -h /home/gateway gateway

COPY --from=builder /out/gateway-provision /usr/local/bin/gateway-provision
COPY --from=builder /out/gateway-centrifugo /usr/local/libexec/gateway/integrations/gateway-centrifugo
COPY --from=builder /out/gateway-mercure /usr/local/libexec/gateway/integrations/gateway-mercure
COPY --from=builder /out/gateway-reverb /usr/local/libexec/gateway/integrations/gateway-reverb

ENV GATEWAY_INTEGRATION_PATHS=/usr/local/libexec/gateway/integrations/gateway-centrifugo:/usr/local/libexec/gateway/integrations/gateway-mercure:/usr/local/libexec/gateway/integrations/gateway-reverb
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/gateway-provision"]

FROM worker-base AS kubernetes
USER root
RUN apk add --no-cache kubectl
COPY scripts/provision-in-cluster /usr/local/bin/gateway-provision-in-cluster
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/gateway-provision-in-cluster"]

FROM worker-base AS docker
# Docker contexts can use the local socket, SSH, or TLS. This target contains
# only the client; it does not start a Docker daemon.
USER root
RUN apk add --no-cache docker-cli openssh-client
USER 10001:10001
