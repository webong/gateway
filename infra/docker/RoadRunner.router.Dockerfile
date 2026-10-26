FROM golang:1.26-alpine AS router-builder

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
FROM php:8.4-cli-alpine AS php-base
RUN apk add --no-cache \
        bash curl libpq oniguruma openssl sqlite-libs su-exec \
    && apk add --no-cache --virtual .gateway-build-deps \
        $PHPIZE_DEPS linux-headers oniguruma-dev postgresql-dev sqlite-dev \
    && docker-php-ext-install -j4 bcmath mbstring pcntl pdo_pgsql pdo_sqlite sockets opcache \
    && apk del .gateway-build-deps \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini" \
    && printf 'opcache.enable_cli=1\n' > "$PHP_INI_DIR/conf.d/router.ini"

FROM php-base AS planner-builder
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

FROM php-base
COPY --from=router-builder /tmp/gateway-router-roadrunner /usr/local/bin/gateway-router-roadrunner
COPY --from=planner-builder /src/gateway/app /gateway/app
COPY --from=planner-builder /src/gateway/pkg /gateway/pkg
COPY --from=planner-builder /src/gateway/scripts/gateway-router /src/gateway/scripts/router-container-entrypoint /src/gateway/scripts/router-start /src/gateway/scripts/router-artisan /gateway/scripts/
COPY --from=planner-builder /src/gateway/vendor /gateway/vendor
WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_PLUGIN_DIR=/data/spinner-extensions \
    GATEWAY_COMPANION_PLUGIN_DIR=/data/planner-extensions \
    GATEWAY_ROUTER_LISTEN=127.0.0.1:8080 \
    GATEWAY_ROUTER_TLS_LISTEN=:8443 \
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
