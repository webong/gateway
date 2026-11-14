# syntax=docker/dockerfile:1
ARG WORKFLOW_TEST_BASE=gateway-workflow-runtime-gate:source
FROM dunglas/frankenphp:1.12.7-builder-php8.3-bookworm AS bridge
COPY pkg/weaver /extension
COPY mod/arranger /mod/arranger
WORKDIR /extension/cmd/bridge
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -mod=readonly -tags=nowatcher,nobrotli,nomercure -o /gateway-workflow-bridge .

FROM ${WORKFLOW_TEST_BASE}
RUN apt-get update && apt-get install -y --no-install-recommends git unzip && rm -rf /var/lib/apt/lists/*
WORKDIR /extension
ENV COMPOSER_ALLOW_SUPERUSER=1 COMPOSER_ROOT_VERSION=0.1.0-dev
COPY pkg/weaver/tests/consumer/composer.json pkg/weaver/tests/consumer/composer.lock pkg/weaver/tests/consumer/composer-source.lock ./
COPY pkg/weaver/register.php ./
RUN composer install --no-plugins --no-interaction --prefer-dist --no-scripts \
    && composer install --no-interaction --prefer-dist --no-scripts
COPY pkg/weaver/src ./src
COPY pkg/weaver/tests ./tests
COPY pkg/weaver/schema ./schema
COPY pkg/weaver/bin ./bin
COPY pkg/weaver/public ./public
COPY pkg/weaver/routes ./routes
COPY pkg/weaver/examples ./examples
COPY pkg/weaver/phpunit.xml ./
COPY pkg/weaver/scripts ./scripts
COPY --from=bridge /gateway-workflow-bridge /usr/local/bin/gateway-workflow-bridge
RUN composer dump-autoload --classmap-authoritative --no-scripts \
    && php scripts/assert-projection.php --compatibility-tests && vendor/bin/workflow help
ENTRYPOINT ["vendor/bin/phpunit", "--fail-on-skipped"]
