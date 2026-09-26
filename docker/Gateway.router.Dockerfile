FROM php:8.4-zts-bookworm AS php-source
FROM golang:1.26-bookworm AS go-toolchain

# Build the PHP shared library and CLI from the upstream release archive.
# The library needs ZTS, embed SAPI, and Linux execution timers.
FROM debian:bookworm-slim AS php-builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential pkg-config xz-utils libcurl4-openssl-dev libonig-dev \
    libsqlite3-dev libssl-dev libxml2-dev zlib1g-dev \
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
    --enable-opcache \
    --enable-pcntl \
    --enable-sockets \
    --with-curl \
    --with-openssl \
    --with-pdo-sqlite \
    --with-sqlite3 \
    --with-zlib \
    && make -j4 \
    && make install \
    && mkdir -p /usr/local/etc/php/conf.d \
    && cp php.ini-production /usr/local/etc/php/php.ini
COPY docker/router.php.ini /usr/local/etc/php/conf.d/router.ini

FROM php-builder AS router-builder

COPY --from=go-toolchain /usr/local/go /usr/local/go
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV PATH="/usr/local/go/bin:${PATH}" GOTELEMETRY=off

COPY . /src/gateway
WORKDIR /src/gateway/ext/frankenphp

RUN CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -mod=readonly -tags=nobrotli,nowatcher,nomercure \
    -o /tmp/gateway-router-library ./cmd/router

FROM debian:bookworm-slim AS php-base

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl libcurl4 libonig5 libsqlite3-0 libssl3 libxml2 zlib1g \
    && rm -rf /var/lib/apt/lists/*
COPY --from=php-builder /usr/local /usr/local
RUN ldconfig

FROM php-base AS planner-builder

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/lib/apt/lists/*
COPY . /src/gateway
WORKDIR /src/gateway/app

RUN GATEWAY_ROUTER_DATA_DIR=/tmp/router-composer-cache \
    composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader

FROM php-base

COPY --from=router-builder /tmp/gateway-router-library /usr/local/bin/gateway-router-library
COPY --from=planner-builder /src/gateway /gateway

WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_ROUTER_LISTEN=:8080 \
    GATEWAY_ROUTER_LIBRARY_BIN=/usr/local/bin/gateway-router-library
RUN mkdir -p /data/cache /data/storage/framework/cache/data \
    /data/storage/framework/sessions /data/storage/framework/views \
    && chown -R www-data:www-data /data \
    && chmod +x /gateway/scripts/gateway-router /gateway/scripts/router-start /gateway/scripts/router-artisan

VOLUME /data
EXPOSE 8080/tcp
USER www-data
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/gateway/scripts/gateway-router", "library"]
