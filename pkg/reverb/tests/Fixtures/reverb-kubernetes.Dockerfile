FROM php:8.4-cli-alpine

RUN apk add --no-cache sqlite-libs \
    && apk add --no-cache --virtual .build-deps $PHPIZE_DEPS linux-headers sqlite-dev \
    && docker-php-ext-install bcmath pcntl pdo_sqlite sockets \
    && apk del .build-deps

WORKDIR /workspace

COPY . /workspace
COPY --from=dependencies . /workspace/vendor

RUN mkdir -p /workspace/tests/Fixtures/octane-app/database

# The source checkout may contain a provider cache from a different host.
RUN rm -f /workspace/tests/Fixtures/octane-app/bootstrap/cache/*.php

COPY --from=smoke reverb.sqlite /workspace/tests/Fixtures/octane-app/database/reverb.sqlite

WORKDIR /workspace/tests/Fixtures/octane-app
