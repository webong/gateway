# RoadRunner extension

`runtime` owns the embedded RoadRunner server and PHP worker pool. It accepts
an HTTP middleware function and imports no Spinner or Gateway implementation.
The independent `cmd/router` module owns the RoadRunner Router composition:
Spinner middleware, automation selection, private provisioning control, and
optional SMTP/DNS listeners. `lib/router` provides only runtime-neutral
Router request routing and the private control handler shared with the
FrankenPHP library runtime; it contains no RoadRunner lifecycle code.

From the repository root:

```sh
composer install --working-dir=app
./scripts/gateway-router roadrunner
```

The launcher generates worker configuration outside `app/`, runs migrations,
and builds this executable if needed. Native distributions contain
`bin/gateway-router-roadrunner`; set `GATEWAY_ROUTER_ROADRUNNER_BIN` to select
another prebuilt executable. No ZTS PHP or FrankenPHP native libraries are needed.
Do not set `GATEWAY_RUNTIME` for the bundled Router: its command selects the adapter.

Standalone Spinner deliberately has no RoadRunner dependency or
`GATEWAY_RUNTIME=roadrunner` mode. This extension's Router command owns the
RoadRunner composition. Build the RoadRunner image with
`docker build -f infra/docker/RoadRunner.router.Dockerfile -t gateway-router:roadrunner-local .`.
It uses the same port 8080 and persistent `/data` convention as the FrankenPHP image.

Test each module separately with `go test ./...` from `ext/roadrunner` and
`ext/roadrunner/cmd/router`. Use `bash scripts/test-router-boundaries` from the
repository root to check extension independence.
