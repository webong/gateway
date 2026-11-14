# syntax=docker/dockerfile:1
ARG GATEWAY_PLANNER_IMAGE=gateway-workflow-host:roadrunner
FROM ${GATEWAY_PLANNER_IMAGE} AS installed
USER root
COPY --from=composer:2 /usr/bin/composer /usr/bin/composer
RUN if command -v apk >/dev/null; then apk add --no-cache git unzip; else apt-get update && apt-get install -y --no-install-recommends git unzip && rm -rf /var/lib/apt/lists/*; fi
COPY pkg/weaver /gateway/pkg/weaver
WORKDIR /gateway/app
ENV COMPOSER_ALLOW_SUPERUSER=1
# This is an actual host consumer. Its root owns repositories, plugin trust and
# the core projection; the dependency package cannot apply that policy for it.
RUN php -r '$p=json_decode(file_get_contents("composer.json"),true,512,JSON_THROW_ON_ERROR); $p["require"]["webong/gateway-weaver"]="dev-main"; $p["repositories"]=[["type"=>"path","url"=>"/gateway/pkg/weaver","options"=>["symlink"=>false,"versions"=>["webong/gateway-weaver"=>"dev-main"]]],["type"=>"vcs","url"=>"https://github.com/webong/workflow","no-api"=>true]]; $p["config"]["allow-plugins"]["webong/composer-source-plugin"]=true; $p["extra"]["source-plugin"]["projections"]["webong/workflow"]=["surfaces"=>["core"]]; file_put_contents("composer.json",json_encode($p,JSON_PRETTY_PRINT|JSON_UNESCAPED_SLASHES));' \
    && composer update webong/gateway-weaver webong/workflow --with-dependencies --no-plugins --no-dev --no-scripts --prefer-dist --no-interaction \
    && composer update webong/gateway-weaver webong/workflow --with-dependencies --no-dev --no-scripts --prefer-dist --no-interaction \
    && composer dump-autoload --no-dev --classmap-authoritative --no-scripts
WORKDIR /gateway

FROM installed AS test
COPY pkg/weaver/tests/planner-smoke.php /planner-smoke.php
ENV GATEWAY_ROUTER_DATA_DIR=/data DB_CONNECTION=sqlite DB_DATABASE=/data/weaver-planner-test.sqlite SESSION_DRIVER=file CACHE_STORE=file
ENTRYPOINT ["php", "/planner-smoke.php"]
