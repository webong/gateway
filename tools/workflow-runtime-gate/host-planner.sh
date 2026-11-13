#!/bin/sh
# Production Planner application, separate from standalone Spinner; disposable state only.
set -eu
export SESSION_DRIVER=file CACHE_STORE=file
mkdir -p /data/storage/framework/cache/data /data/storage/framework/sessions /data/storage/framework/views /data/cache
touch /data/gateway.sqlite
php /gateway/app/artisan migrate --realpath --path=/gateway/vendor/webong/proxy/database/migrations --force
php /gateway/app/artisan gateway:migrate
php /gateway/app/artisan migrate --force
exec php -S 0.0.0.0:8080 -t /gateway/app/public /gateway/app/index.php
