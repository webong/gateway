# Standalone Spinner host image.
#
# The host binary (pkg/spinner/cmd/gateway) composes the static automation
# guests listed in pkg/spinner/cmd/gateway/plugins.txt. The php and hookscript
# guests embed PHP through FrankenPHP, so the builder needs ZTS PHP with the
# embed SAPI and php-config, exactly like the Router image.
FROM php:8.4-zts-bookworm AS php-source
FROM golang:1.26-bookworm AS go-toolchain

FROM debian:bookworm-slim AS php-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config xz-utils libcurl4-openssl-dev libonig-dev \
    libpq-dev libsqlite3-dev libssl-dev libxml2-dev zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*
COPY --from=php-source /usr/src/php.tar.xz /tmp/php.tar.xz
RUN mkdir -p /usr/src/php \
    && tar -xJf /tmp/php.tar.xz -C /usr/src/php --strip-components=1
WORKDIR /usr/src/php
RUN ./configure \
    --prefix=/usr/local \
    --with-config-file-path=/usr/local/etc/php \
    --with-config-file-scan-dir=/usr/local/etc/php/conf.d \
    --enable-embed \
    --enable-zts \
    --disable-zend-signals \
    --enable-zend-max-execution-timers \
    --disable-cgi \
    --disable-phpdbg \
    --without-pear \
    --enable-bcmath \
    --enable-mbstring \
    --enable-pcntl \
    --enable-sockets \
    --with-curl \
    --with-openssl \
    --with-pdo-sqlite \
    --with-pdo-pgsql \
    --with-sqlite3 \
    --with-zlib \
    && make -j4 \
    && make install

FROM php-builder AS host-builder

COPY --from=go-toolchain /usr/local/go /usr/local/go
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV PATH="/usr/local/go/bin:${PATH}" GOTELEMETRY=off

# FrankenPHP optional features not used by Spinner are disabled to keep the
# build small and dependency-free.
ARG GATEWAY_BUILD_TAGS="nowatcher,nobrotli,nomercure"

COPY . /src/gateway
WORKDIR /src/gateway/pkg/spinner/cmd/gateway
RUN CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -mod=readonly -tags="${GATEWAY_BUILD_TAGS}" -o /tmp/gateway .

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl libcurl4 libonig5 libpq5 libsqlite3-0 libssl3 libxml2 zlib1g \
    && rm -rf /var/lib/apt/lists/*
COPY --from=php-builder /usr/local /usr/local
RUN ldconfig \
    && groupadd -g 10001 gateway \
    && useradd -u 10001 -g gateway -d /app gateway

WORKDIR /app
COPY --from=host-builder /tmp/gateway /app/gateway

USER 10001:10001

# HTTP ingress plus the unprivileged authoritative DNS ports used by the
# production compose example. Publish both DNS transports on public port 53.
EXPOSE 5001/tcp 2525/tcp 5353/tcp 5353/udp

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -fsS http://127.0.0.1:5001/health >/dev/null || exit 1

ENTRYPOINT ["/app/gateway"]
