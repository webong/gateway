FROM composer:2 AS dependencies

WORKDIR /app

COPY composer.json composer.lock ./

RUN composer install --no-dev --no-interaction --no-progress --prefer-dist --ignore-platform-req=ext-sockets \
    && composer dump-autoload --dev --no-interaction

FROM php:8.4-cli-alpine

RUN apk add --no-cache sqlite-libs \
    && apk add --no-cache --virtual .build-deps $PHPIZE_DEPS sqlite-dev \
    && docker-php-ext-install pdo_sqlite \
    && apk del .build-deps

WORKDIR /app

COPY --from=dependencies /app/vendor /app/vendor
COPY . .

# The source fixture may carry a locally generated provider manifest that
# references optional Octane packages. This image deliberately runs the
# planner over ordinary PHP HTTP, so let Laravel regenerate its cache.
RUN rm -f /app/tests/Fixtures/octane-app/bootstrap/cache/*.php

WORKDIR /app/tests/Fixtures/octane-app

CMD ["sh", "-c", "touch /tmp/registry.sqlite && exec php -S 0.0.0.0:8080 -t public public/index.php"]
