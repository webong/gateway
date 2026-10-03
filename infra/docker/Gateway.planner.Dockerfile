FROM dunglas/frankenphp:php8.4-bookworm AS php-base

RUN apt-get update \
    && apt-get install -y --no-install-recommends libpq-dev \
    && docker-php-ext-install -j4 bcmath pcntl pdo_pgsql sockets \
    && apt-mark manual libpq5 \
    && apt-get purge -y --auto-remove libpq-dev \
    && rm -rf /var/lib/apt/lists/* \
    && cp "$PHP_INI_DIR/php.ini-production" "$PHP_INI_DIR/php.ini"

FROM php-base AS planner-builder

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update \
    && apt-get install -y --no-install-recommends unzip \
    && rm -rf /var/lib/apt/lists/*
COPY . /gateway
WORKDIR /gateway/app
RUN composer install --no-dev --no-interaction --prefer-dist --no-plugins --no-scripts \
    && GATEWAY_ROUTER_DATA_DIR=/tmp/gateway-planner-composer \
        composer dump-autoload --no-dev --no-interaction --optimize

FROM php-base

COPY --from=planner-builder /gateway /gateway
COPY app/Caddyfile.planner /etc/caddy/Caddyfile
COPY scripts/planner-start /usr/local/bin/gateway-planner-start

RUN mkdir -p /data /config /tmp \
    && chown -R www-data:www-data /data /config /tmp \
    && setcap -r /usr/local/bin/frankenphp \
    && chmod +x /usr/local/bin/gateway-planner-start

WORKDIR /gateway/app
ENV GATEWAY_ROUTER_DATA_DIR=/data/planner \
    GATEWAY_PATH_RESOLVER='Gateway\Router\EndpointPathResolver' \
    APP_ENV=production \
    DB_CONNECTION=pgsql \
    SESSION_DRIVER=file \
    GATEWAY_CACHE_STORE=database \
    GATEWAY_MUTATION_LOCK_STORE=database
EXPOSE 8080/tcp
USER www-data
ENTRYPOINT ["/usr/local/bin/gateway-planner-start"]
