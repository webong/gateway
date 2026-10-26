# syntax=docker/dockerfile:1.7

# Select the combined Router runtime with --build-arg GATEWAY_ROUTER_RUNTIME=...
# Supported values are roadrunner (default) and frankenphp.
ARG GATEWAY_ROUTER_RUNTIME=roadrunner

FROM golang:1.26-alpine AS roadrunner-builder
ARG GATEWAY_BUILD_TAGS=""
ENV GOTELEMETRY=off
RUN apk add --no-cache build-base
COPY . /src/gateway
WORKDIR /src/gateway/pkg/roadrunner/cmd/router
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -mod=readonly -trimpath -ldflags="-s -w" \
        -tags="${GATEWAY_BUILD_TAGS}" -o /tmp/gateway-router-roadrunner .

# RoadRunner uses ordinary PHP CLI workers; no ZTS or embed SAPI is required.
FROM php:8.4-cli-alpine AS roadrunner-php-base
RUN apk add --no-cache \
        bash curl libpq oniguruma openssl sqlite-libs su-exec \
    && apk add --no-cache --virtual .gateway-build-deps \
        $PHPIZE_DEPS linux-headers oniguruma-dev postgresql-dev sqlite-dev \
    && docker-php-ext-install -j4 bcmath mbstring pcntl pdo_pgsql pdo_sqlite sockets opcache \
    && apk del .gateway-build-deps \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" \
    && printf 'opcache.enable_cli=1\n' > "$PHP_INI_DIR/conf.d/router.ini"

FROM roadrunner-php-base AS roadrunner-planner-builder
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apk add --no-cache unzip
COPY . /src/gateway
WORKDIR /src/gateway/app
# Install locked dependencies without the merge plugin's post-install update.
# Enable it for autoload generation so extension namespaces and discovery remain.
RUN --mount=type=cache,target=/tmp/composer-cache \
    COMPOSER_CACHE_DIR=/tmp/composer-cache \
    composer install --no-dev --no-interaction --prefer-dist --no-plugins --no-scripts \
    && GATEWAY_ROUTER_DATA_DIR=/tmp/router-composer-cache \
        composer dump-autoload --no-dev --no-interaction --optimize

FROM roadrunner-php-base AS roadrunner
COPY --from=roadrunner-builder /tmp/gateway-router-roadrunner /usr/local/bin/gateway-router-roadrunner
COPY --from=roadrunner-planner-builder /src/gateway/app /gateway/app
COPY --from=roadrunner-planner-builder /src/gateway/pkg /gateway/pkg
COPY --from=roadrunner-planner-builder /src/gateway/scripts/gateway-router /src/gateway/scripts/router-container-entrypoint /src/gateway/scripts/router-start /src/gateway/scripts/router-artisan /gateway/scripts/
COPY --from=roadrunner-planner-builder /src/gateway/vendor /gateway/vendor
WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_PLUGIN_DIR=/data/spinner-extensions \
    GATEWAY_COMPANION_PLUGIN_DIR=/data/planner-extensions \
    GATEWAY_ROUTER_LISTEN=127.0.0.1:8080 \
    GATEWAY_SHELL_HOOKS_DIR=/data/hooks \
    GATEWAY_ROUTER_ROADRUNNER_BIN=/usr/local/bin/gateway-router-roadrunner
RUN mkdir -p /data/cache /data/hooks /data/spinner-extensions /data/planner-extensions /data/storage/framework/cache/data \
        /data/storage/framework/sessions /data/storage/framework/views \
    && command -v openssl \
    && chown -R www-data:www-data /data \
    && printf 'variables_order=EGPCS\n' > /usr/local/etc/php/conf.d/roadrunner.ini \
    && chmod +x /gateway/scripts/gateway-router /gateway/scripts/router-container-entrypoint \
        /gateway/scripts/router-start /gateway/scripts/router-artisan
VOLUME /data
EXPOSE 8443/tcp 2525/tcp 993/tcp 995/tcp
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD su-exec www-data curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/gateway/scripts/router-container-entrypoint", "roadrunner"]

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
    --enable-opcache \
    --enable-pcntl \
    --enable-sockets \
    --with-curl \
    --with-openssl \
    --with-pdo-sqlite \
    --with-pdo-pgsql \
    --with-sqlite3 \
    --with-zlib \
    && make -j4 \
    && make install \
    && mkdir -p /usr/local/etc/php/conf.d \
    && cp php.ini-production /usr/local/etc/php/php.ini
COPY infra/docker/router.php.ini /usr/local/etc/php/conf.d/router.ini

FROM php-builder AS frankenphp-router-builder
COPY --from=go-toolchain /usr/local/go /usr/local/go
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV PATH="/usr/local/go/bin:${PATH}" GOTELEMETRY=off
COPY . /src/gateway
WORKDIR /src/gateway/pkg/frankenphp/cmd/router
ARG GATEWAY_BUILD_TAGS=""
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -mod=readonly -tags="nobrotli,nowatcher,nomercure,${GATEWAY_BUILD_TAGS}" \
    -o /tmp/gateway-router-library .

FROM debian:bookworm-slim AS frankenphp-php-base
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl gosu libcurl4 libonig5 libpq5 libsqlite3-0 libssl3 libxml2 zlib1g \
    && rm -rf /var/lib/apt/lists/*
COPY --from=php-builder /usr/local /usr/local
RUN ldconfig

FROM frankenphp-php-base AS frankenphp-planner-builder
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/lib/apt/lists/*
COPY . /src/gateway
WORKDIR /src/gateway/app
# Install only the Planner host and its built-in integrations. Automation
# runtimes and companions arrive later through verified extension packages.
RUN --mount=type=cache,target=/tmp/composer-cache \
    COMPOSER_CACHE_DIR=/tmp/composer-cache \
        composer install --no-dev --no-interaction --prefer-dist --no-plugins --no-scripts \
    && GATEWAY_ROUTER_DATA_DIR=/tmp/router-composer-cache \
        composer dump-autoload --no-dev --no-interaction --optimize

FROM frankenphp-php-base AS frankenphp
COPY --from=frankenphp-router-builder /tmp/gateway-router-library /usr/local/bin/gateway-router-library
COPY --from=frankenphp-planner-builder /src/gateway /gateway
WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_PLUGIN_DIR=/data/spinner-extensions \
    GATEWAY_COMPANION_PLUGIN_DIR=/data/planner-extensions \
    GATEWAY_ROUTER_LISTEN=127.0.0.1:8080 \
    GATEWAY_ROUTER_TLS_LISTEN=:8443 \
    GATEWAY_ROUTER_TLS_NAME=gateway \
    GATEWAY_COMPANION_PHP_BINARY=/usr/local/bin/php \
    GATEWAY_ROUTER_LIBRARY_BIN=/usr/local/bin/gateway-router-library
RUN mkdir -p /data/cache /data/spinner-extensions /data/planner-extensions \
    /data/storage/framework/cache/data \
    /data/storage/framework/sessions /data/storage/framework/views \
    && chown -R www-data:www-data /data \
    && chmod +x /gateway/scripts/gateway-router /gateway/scripts/router-container-entrypoint \
        /gateway/scripts/router-start /gateway/scripts/router-artisan
VOLUME /data
EXPOSE 8080/tcp 8443/tcp 2525/tcp 993/tcp 995/tcp
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD gosu www-data curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/gateway/scripts/router-container-entrypoint", "library"]

# Both runtime variants carry Planner and Spinner. BuildKit builds only the
# selected runtime branch and its dependencies.
FROM ${GATEWAY_ROUTER_RUNTIME} AS selected-router
ARG GATEWAY_ROUTER_RUNTIME
LABEL org.opencontainers.image.title="Gateway Router" \
      com.webong.gateway.router-runtime="${GATEWAY_ROUTER_RUNTIME}"
