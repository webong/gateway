FROM dunglas/frankenphp:builder AS spinner-builder

COPY --from=caddy:builder /usr/bin/xcaddy /usr/bin/xcaddy
COPY . /src/gateway
WORKDIR /src/gateway

RUN CGO_ENABLED=1 \
    CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    xcaddy build \
        --output /tmp/frankenphp \
        --with github.com/dunglas/frankenphp/caddy \
        --with github.com/webong/gateway/src/spinner/cmd/bridge/caddy \
        --replace github.com/webong/gateway=/src/gateway

FROM dunglas/frankenphp AS php-base

RUN docker-php-ext-install sockets

FROM php-base AS planner-builder

COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN apt-get update && apt-get install -y --no-install-recommends unzip
COPY . /src/gateway
WORKDIR /src/gateway/app

RUN GATEWAY_ROUTER_DATA_DIR=/tmp/router-composer-cache \
    composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader

FROM php-base

COPY --from=spinner-builder /tmp/frankenphp /usr/local/bin/frankenphp
COPY --from=planner-builder /src/gateway /gateway

WORKDIR /gateway
ENV GATEWAY_ROUTER_DATA_DIR=/data \
    GATEWAY_ROUTER_LISTEN=:8080
RUN mkdir -p /data/cache /data/storage/framework/cache/data \
    /data/storage/framework/sessions /data/storage/framework/views \
    && chmod +x /gateway/scripts/gateway-router /gateway/scripts/router-start /gateway/scripts/router-artisan

VOLUME /data
EXPOSE 8080/tcp
ENTRYPOINT ["/gateway/scripts/gateway-router", "frankenphp"]
