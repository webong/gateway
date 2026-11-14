#!/bin/sh
# Production Planner application, separate from standalone Spinner; disposable state only.
set -eu
export SESSION_DRIVER=file CACHE_STORE=file
mkdir -p /data/storage/framework/cache/data /data/storage/framework/sessions /data/storage/framework/views /data/cache
touch /data/gateway.sqlite
php /gateway/app/planner/artisan migrate --realpath --path=/gateway/vendor/webong/proxy/database/migrations --force
php /gateway/app/planner/artisan gateway:migrate
php /gateway/app/planner/artisan migrate --force
exec php -S 0.0.0.0:8080 -t /gateway/app/planner/public /gateway/app/planner/public/index.php
