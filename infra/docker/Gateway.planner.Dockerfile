FROM dunglas/frankenphp:php8.4-bookworm AS php-base

ARG SWOOLE_VERSION=6.2.3
RUN install-php-extensions bcmath pcntl pdo_pgsql sockets \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"
RUN apt-get update \
    && apt-get install -y --no-install-recommends $PHPIZE_DEPS g++ libssl-dev \
    && yes no | pecl install "swoole-${SWOOLE_VERSION}" \
    && docker-php-ext-enable swoole \
    && apt-get purge -y --auto-remove $PHPIZE_DEPS g++ libssl-dev \
    && rm -rf /var/lib/apt/lists/* /tmp/pear

FROM ghcr.io/roadrunner-server/roadrunner:2025.1.15 AS roadrunner

FROM php-base AS planner-builder

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update \
    && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/mod/apt/lists/*
COPY . /gateway
WORKDIR /gateway/app
RUN --mount=type=cache,target=/tmp/composer-cache \
    COMPOSER_CACHE_DIR=/tmp/composer-cache \
    composer install --no-dev --no-interaction --prefer-dist --no-plugins --no-scripts \
    && GATEWAY_ROUTER_DATA_DIR=/tmp/gateway-planner-composer \
        composer dump-autoload --no-dev --no-interaction --optimize

FROM php-base

COPY --from=planner-builder /gateway /gateway
COPY --from=roadrunner /usr/bin/rr /gateway/app/rr
COPY app/Caddyfile.planner /etc/caddy/Caddyfile
COPY scripts/planner-start /usr/local/bin/gateway-planner-start

RUN mkdir -p /data /config /tmp \
    && chown -R www-data:www-data /data /config /tmp \
    && ln -s ../vendor /gateway/app/vendor \
    && test -x /gateway/app/rr \
    && php -r 'exit(extension_loaded("swoole") ? 0 : 1);' \
    && setcap -r /usr/local/bin/frankenphp \
    && chmod +x /usr/local/bin/gateway-planner-start

WORKDIR /gateway/app
ENV GATEWAY_ROUTER_DATA_DIR=/data/planner \
    GATEWAY_PLANNER_SERVER=frankenphp \
    GATEWAY_PATH_RESOLVER='Gateway\Router\EndpointPathResolver' \
    APP_ENV=production \
    DB_CONNECTION=pgsql \
    SESSION_DRIVER=file \
    GATEWAY_CACHE_STORE=database \
    GATEWAY_MUTATION_LOCK_STORE=database
EXPOSE 8080/tcp
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1
USER www-data
ENTRYPOINT ["/usr/local/bin/gateway-planner-start"]
