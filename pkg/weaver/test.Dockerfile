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
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*
WORKDIR /extension
COPY pkg/weaver/composer.json pkg/weaver/composer.lock ./
RUN composer install --no-interaction --prefer-dist --no-scripts
COPY pkg/weaver ./
COPY --from=bridge /gateway-workflow-bridge /usr/local/bin/gateway-workflow-bridge
RUN composer dump-autoload --optimize --no-scripts
ENTRYPOINT ["vendor/bin/phpunit", "--fail-on-skipped"]
