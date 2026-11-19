#!/bin/sh
# Production Planner application, separate from standalone Spinner; disposable state only.
set -eu
export SESSION_DRIVER=file CACHE_STORE=file
mkdir -p /data/storage/framework/cache/data /data/storage/framework/sessions /data/storage/framework/views /data/cache
touch /data/gateway.sqlite
php /gateway/app/planner/artisan gateway:migrate --force
exec php -S 0.0.0.0:8080 -t /gateway/app/planner/public /gateway/app/planner/public/index.php
