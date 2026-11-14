# syntax=docker/dockerfile:1.7
ARG WORKFLOW_IMAGE=ghcr.io/webong/workflow@sha256:dc876bfa06d295e4049a5658ef813d56a536db22c069da674633151664813d9e
FROM dunglas/frankenphp:1.12.7-builder-php8.3-bookworm@sha256:668b927010b6ce7a623e70c9cd6a4ba91a4763184e3c506596c828a07d0560db AS bridge
ARG TARGETARCH
COPY pkg/weaver /extension
COPY mod/arranger /mod/arranger
WORKDIR /extension/cmd/bridge
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -trimpath -buildvcs=false -mod=readonly -tags=nowatcher,nobrotli,nomercure -o /gateway-workflow-bridge .

WORKDIR /extension
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH="$TARGETARCH" \
    go build -trimpath -buildvcs=false -mod=readonly -o /gateway-weaver-spinner ./cmd/spinner

FROM ${WORKFLOW_IMAGE} AS package
ARG TARGETARCH
ARG WEAVER_REVISION=development
ARG WEAVER_VERSION=0.1.0-dev
ARG WORKFLOW_IMAGE
COPY --from=bridge /gateway-workflow-bridge /usr/local/bin/workflowd
COPY tools/workflow-runtime-gate/bundle.php /bundle.php
RUN php /bundle.php
WORKDIR /extension
COPY pkg/weaver/composer.json pkg/weaver/composer.lock pkg/weaver/composer-source.lock pkg/weaver/register.php ./
ENV COMPOSER_ALLOW_SUPERUSER=1 COMPOSER_ROOT_VERSION=${WEAVER_VERSION}
RUN composer install --no-plugins --no-dev --no-interaction --prefer-dist --no-scripts \
    && composer install --no-dev --no-interaction --prefer-dist --no-scripts
COPY pkg/weaver/src /extension/src
COPY pkg/weaver/scripts/assert-projection.php /extension/scripts/assert-projection.php
RUN composer dump-autoload --no-dev --classmap-authoritative --no-scripts \
    && php scripts/assert-projection.php && vendor/bin/workflow help \
    && find vendor -type d -name .git -prune -exec rm -rf {} +
COPY pkg/weaver/src /package/src
COPY pkg/weaver/schema /package/schema
COPY pkg/weaver/bin /package/bin
COPY pkg/weaver/routes /package/routes
COPY pkg/weaver/register.php /package/register.php
COPY pkg/weaver/public /package/public
COPY pkg/weaver/LICENSE pkg/weaver/README.md pkg/weaver/UPGRADE.md pkg/weaver/HTTP.md pkg/weaver/composer.json pkg/weaver/composer.lock pkg/weaver/composer-source.lock /package/
COPY --from=bridge /gateway-weaver-spinner /package/bin/spinner
RUN cp -a vendor /package/vendor && mkdir -p /package/empty-tmp && chmod 1777 /package/empty-tmp
COPY pkg/weaver/manifest.php /manifest.php
RUN TARGETARCH="$TARGETARCH" WEAVER_REVISION="$WEAVER_REVISION" WEAVER_VERSION="$WEAVER_VERSION" WEAVER_RUNTIME_IMAGE="$WORKFLOW_IMAGE" php /manifest.php

FROM scratch
COPY --from=package /package /package
COPY --from=package --chmod=1777 /package/empty-tmp /tmp
WORKDIR /package
ENTRYPOINT ["/package/bin/spinner"]
