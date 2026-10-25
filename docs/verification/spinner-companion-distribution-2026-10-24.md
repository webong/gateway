# Spinner companion distribution verification — 2026-10-24

> Historical result: the static automation composition described below was
> removed on 2026-10-25. Current Gateway hosts contain no concrete automation
> runtimes; see `docs/automations.md` for the installable package lifecycle.

This record covers an uncommitted local tree on `main`, based on `a3cf487`, on
an ARM64 OrbStack Docker host. It does not describe a pushed commit, published
image, or deployed environment.

## Dependency and package checks

The standalone Spinner composition was regenerated from
`pkg/spinner/cmd/gateway/plugins.txt`. Its module-local dependency graph had no
match for FrankenPHP, `pkg/php`, or `pkg/hookscript`:

```bash
cd pkg/spinner/cmd/gateway
GOWORK=off GOCACHE=/private/tmp/gateway-go-cache go list -deps .
```

The actual combined HookScript package was built after Spinner was already
running:

```bash
cd pkg/hookscript
php scripts/build-ext-package \
  /private/tmp/gateway-hookscript-release-20261024/webong%2fhookscript \
  1.0.0 smoke-r1
tar -C /private/tmp/gateway-hookscript-release-20261024 \
  -czf /private/tmp/webong-hookscript-1.0.0.tar.gz webong%2fhookscript
```

The result was a 1.5 MiB archive with SHA-256
`4f1db82e218e91649051af36dba4467e3cf9ed951bffa4ae998da740d5938a3f`.
Its manifest contains named `spinner` (`process`) and `planner`
(`php-jsonline`) entrypoints.

## Regression suites

```bash
cd pkg/spinner
GOCACHE=/private/tmp/gateway-go-cache go test ./...

cd pkg/spinner/cmd/gateway
GOWORK=off GOCACHE=/private/tmp/gateway-go-cache go test ./...

cd pkg/hookscript
GOCACHE=/private/tmp/gateway-go-cache go test ./...

cd ../..
php vendor/bin/pest \
  tests/Feature/CompanionExtensionActivationTest.php \
  tests/Feature/ExtCompanionProcessTest.php \
  tests/Feature/CompanionActionTest.php \
  tests/Feature/ExtensionArtifactReconciliationTest.php --compact
```

Results: every Go suite passed. The focused Planner suite passed 30 tests with
150 assertions. The regression covers Planner-owned artifact desired state,
transport and package integrity, independent named-entrypoint selection,
post-start Spinner installation, actual HookScript PHP invocation, static to
dynamic and dynamic to static selection, deactivation, stale-cache removal,
and failure closed when desired state or artifacts are unavailable.

The native FrankenPHP Router module was not a valid host-side comparison on
this machine: its non-container build requires the watcher C headers. The
RoadRunner composition suite passed. This limitation does not affect the
standalone Spinner build below.

## Image build and smoke

```bash
docker build \
  -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-spinner:pure-go-local .

docker run -d --name gateway-spinner-pure-go-smoke \
  -p 127.0.0.1:5501:5001 \
  -e GATEWAY_RUNTIME=diagnostic \
  -e GATEWAY_AUTOMATION_ENGINES=javascript,lua,go \
  gateway-spinner:pure-go-local

curl -fsS http://127.0.0.1:5501/health
curl -sS -o /tmp/spinner-ready -w '%{http_code}' \
  http://127.0.0.1:5501/ready
docker exec gateway-spinner-pure-go-smoke sh -c \
  'command -v php || true; ldd /app/gateway; test -w /data/extensions'
docker image inspect gateway-spinner:pure-go-local \
  --format '{{.Id}} {{.Size}} {{.Architecture}}'
```

Results:

- image ID: `sha256:5c5f466e75e3f7bf7139a7354af5cbf07653e9635add7ac6b587af46f5d931f7`
- architecture: `arm64`
- image size: `150118643` bytes (about 143.2 MiB)
- stripped Spinner binary: `38054096` bytes (about 36.3 MiB)
- `/health`: `200 OK` with body `OK`
- `/ready`: `204 No Content`
- PHP executable: absent
- linked `libphp`: absent
- `/data/extensions`: writable by the non-root Spinner user
- requesting `GATEWAY_AUTOMATION_ENGINES=hookscript` fails at startup because
  HookScript is not compiled into the default image

There is no successful local PHP-embedded Spinner image to compare byte for
byte. The historical Dockerfile built ZTS/embed PHP and copied all of
`/usr/local` into the runtime, but that source does not produce a valid image:
`pkg/php/runtime` calls the nonexistent FrankenPHP 1.12.7 API
`frankenphp.ExecuteScript`. The repository therefore has an exact verified
PHP-free size, but no honest verified baseline size for the superseded image.
