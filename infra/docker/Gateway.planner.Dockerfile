ARG SWOOLE_VERSION=6.2.3

FROM dunglas/frankenphp:php8.4-bookworm AS frankenphp

RUN install-php-extensions bcmath pcntl pdo_pgsql sockets

FROM php:8.4-fpm-bookworm AS fpm

FROM caddy:2 AS caddy

FROM php:8.4-cli-bookworm AS php-base

ARG SWOOLE_VERSION
COPY --from=frankenphp /usr/local/bin/install-php-extensions /usr/local/bin/install-php-extensions
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
    && rm -rf /var/lib/apt/lists/*
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
COPY --from=fpm /usr/local/sbin/php-fpm /usr/local/sbin/php-fpm
COPY --from=caddy /usr/bin/caddy /usr/local/bin/caddy
COPY --from=frankenphp /usr/local/bin/frankenphp /usr/local/bin/frankenphp-real
COPY --from=frankenphp /usr/local/lib/libphp.so /usr/local/lib/libphp.so
COPY --from=frankenphp /usr/local/lib/libwatcher-c.so.0 /usr/local/lib/libwatcher-c.so.0
COPY --from=frankenphp /usr/local/lib/php/extensions /opt/frankenphp/extensions
COPY --from=frankenphp /usr/local/etc/php/conf.d /opt/frankenphp/conf.d
COPY app/Caddyfile.planner /etc/caddy/Caddyfile
COPY app/Caddyfile.fpm /etc/caddy/Caddyfile.fpm
COPY infra/docker/php-fpm.gateway.conf /etc/php-fpm.d/gateway.conf
COPY infra/docker/frankenphp /usr/local/bin/frankenphp
COPY infra/docker/php-frankenphp.ini /opt/frankenphp/php.ini
COPY scripts/planner-start /usr/local/bin/gateway-planner-start

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        libargon2-1 libbrotli1 libcurl4 libicu72 libldap-2.5-0 libonig5 \
        libreadline8 librtmp1 libsasl2-2 libsqlite3-0 libssh2-1 libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && ldconfig \
    && mkdir -p /data /config /tmp /gateway/app/bootstrap/cache \
    && chown -R www-data:www-data /data /config /tmp /gateway/app/bootstrap/cache \
    && ln -s ../vendor /gateway/app/vendor \
    && test -x /gateway/app/rr \
    && test -x /usr/local/bin/caddy \
    && php-fpm -t --fpm-config /etc/php-fpm.d/gateway.conf \
    && ldd /usr/local/bin/frankenphp-real > /tmp/frankenphp-ldd \
    && ! grep -q 'not found' /tmp/frankenphp-ldd \
    && rm /tmp/frankenphp-ldd \
    && chmod +x /usr/local/bin/frankenphp /usr/local/bin/gateway-planner-start \
    && frankenphp version \
    && php -r 'exit(extension_loaded("swoole") ? 0 : 1);'

WORKDIR /gateway/app
ENV GATEWAY_ROUTER_DATA_DIR=/data/planner \
    GATEWAY_PLANNER_SERVER=frankenphp \
    GATEWAY_PATH_RESOLVER='Gateway\Router\EndpointPathResolver' \
    XDG_CONFIG_HOME=/config \
    XDG_DATA_HOME=/data \
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
