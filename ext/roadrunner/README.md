# RoadRunner extension

`runtime` owns the embedded RoadRunner server and PHP worker pool. It accepts
an HTTP middleware function and imports no Spinner or Gateway implementation.
The independent `cmd/router` module connects it to `lib/router`, which owns
Spinner composition, automation selection, and optional SMTP/DNS listeners.

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

The old Spinner `GATEWAY_RUNTIME=roadrunner` entrypoint remains compatible and
delegates server ownership to this extension. It is not used by the new bundled
launcher. The existing Router Docker image still selects FrankenPHP.

Test each module separately with `go test ./...` from `ext/roadrunner` and
`ext/roadrunner/cmd/router`. Use `bash scripts/test-router-boundaries` from the
repository root to check extension independence.
