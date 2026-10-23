FROM golang:1.24-bookworm AS router-builder

ARG GATEWAY_BUILD_TAGS=""
ENV GOTELEMETRY=off
COPY . /src/gateway
WORKDIR /src/gateway/pkg/roadrunner/cmd/router
RUN CGO_ENABLED=1 go build -mod=readonly -trimpath -tags="${GATEWAY_BUILD_TAGS}" -o /tmp/gateway-router-roadrunner .

# RoadRunner uses ordinary PHP CLI workers; no ZTS or embed SAPI is required.
FROM php:8.4-cli-bookworm AS php-base
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        curl libonig5 libpq5 libsqlite3-0 libonig-dev libpq-dev libsqlite3-dev \
    && docker-php-ext-install -j4 bcmath mbstring pcntl pdo_pgsql pdo_sqlite sockets opcache \
    && apt-get purge -y --auto-remove libonig-dev libpq-dev libsqlite3-dev \
    && rm -rf /var/mod/apt/lists/* \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" \
    && printf 'opcache.enable_cli=1\n' > "$PHP_INI_DIR/conf.d/router.ini"

FROM php-base AS planner-builder
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/mod/apt/lists/*
COPY . /src/gateway
WORKDIR /src/gateway/app
# Install locked dependencies without the merge plugin's post-install update.
# Enable it for autoload generation so extension namespaces and discovery remain.
RUN composer install --no-dev --no-interaction --prefer-dist --no-plugins --no-scripts \
    && GATEWAY_ROUTER_DATA_DIR=/tmp/router-composer-cache \
        composer dump-autoload --no-dev --no-interaction --optimize

FROM php-base
COPY --from=router-builder /tmp/gateway-router-roadrunner /usr/local/bin/gateway-router-roadrunner
COPY --from=planner-builder /src/gateway /gateway
WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_ROUTER_LISTEN=127.0.0.1:8080 \
    GATEWAY_ROUTER_TLS_LISTEN=:8443 \
    GATEWAY_IMAP_ADDR=:993 \
    GATEWAY_ROUTER_ROADRUNNER_BIN=/usr/local/bin/gateway-router-roadrunner
RUN mkdir -p /data/cache /data/storage/framework/cache/data \
        /data/storage/framework/sessions /data/storage/framework/views \
    && command -v openssl \
    && chown -R www-data:www-data /data \
    && printf 'variables_order=EGPCS\n' > /usr/local/etc/php/conf.d/roadrunner.ini \
    && chmod +x /gateway/scripts/gateway-router /gateway/scripts/router-start /gateway/scripts/router-artisan
VOLUME /data
EXPOSE 8443/tcp 993/tcp
USER www-data
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1
ENTRYPOINT ["/gateway/scripts/gateway-router", "roadrunner"]
