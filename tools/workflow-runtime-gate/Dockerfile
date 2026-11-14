# syntax=docker/dockerfile:1
ARG WORKFLOW_IMAGE=gateway-workflow-runtime-gate:source
FROM ${WORKFLOW_IMAGE} AS bundle
ARG TARGETARCH
COPY tools/workflow-runtime-gate/bundle.php /bundle.php
RUN php /bundle.php
COPY tools/workflow-runtime-gate/build/spinner /package/bin/spinner
COPY tools/workflow-runtime-gate/gate /package/gate
COPY --from=workflow app/schema/sqlite.sql /package/schema/sqlite.sql
RUN cp -a /app /package/app
COPY tools/workflow-runtime-gate/manifest.php /manifest.php
RUN mkdir -p /package/empty-tmp && php /manifest.php

# No system PHP, shell, loader, shared libraries or toolchain: only the closure.
FROM scratch
COPY --from=bundle /package /package
COPY --from=bundle /package/empty-tmp /tmp
WORKDIR /package
ENTRYPOINT ["/package/bin/spinner"]
