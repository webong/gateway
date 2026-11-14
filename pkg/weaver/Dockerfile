# syntax=docker/dockerfile:1.7
ARG WORKFLOW_IMAGE=gateway-workflow-runtime-gate:source
FROM dunglas/frankenphp:1.12.7-builder-php8.3-bookworm AS bridge
COPY pkg/weaver /extension
COPY mod/arranger /mod/arranger
WORKDIR /extension/cmd/bridge
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 CGO_CFLAGS="$(php-config --includes)" \
    CGO_LDFLAGS="$(php-config --ldflags) $(php-config --libs)" \
    go build -mod=readonly -tags=nowatcher,nobrotli,nomercure -o /gateway-workflow-bridge .

FROM ${WORKFLOW_IMAGE} AS package
ARG TARGETARCH
ARG WEAVER_REVISION=development
COPY --from=bridge /gateway-workflow-bridge /usr/local/bin/workflowd
COPY tools/workflow-runtime-gate/bundle.php /bundle.php
RUN php /bundle.php
RUN apt-get update && apt-get install -y --no-install-recommends git && rm -rf /var/lib/apt/lists/*
WORKDIR /extension
COPY pkg/weaver/composer.json pkg/weaver/composer.lock ./
RUN composer install --no-dev --no-interaction --prefer-dist --no-scripts \
    && find vendor -type d -name .git -prune -exec rm -rf {} +
COPY pkg/weaver/src /package/src
COPY pkg/weaver/schema /package/schema
COPY pkg/weaver/bin /package/bin
COPY pkg/weaver/public /package/public
COPY pkg/weaver/LICENSE pkg/weaver/composer.json pkg/weaver/composer.lock /package/
COPY pkg/weaver/build/spinner /package/bin/spinner
RUN cp -a vendor /package/vendor && mkdir -p /package/empty-tmp && chmod 1777 /package/empty-tmp
COPY pkg/weaver/manifest.php /manifest.php
RUN TARGETARCH="$TARGETARCH" WEAVER_REVISION="$WEAVER_REVISION" php /manifest.php

FROM scratch
COPY --from=package /package /package
COPY --from=package --chmod=1777 /package/empty-tmp /tmp
WORKDIR /package
ENTRYPOINT ["/package/bin/spinner"]
