# FrankenPHP Router release-image verification — 2026-10-26

This verification covers a local tree on `main`, based on `061497a2`, on an
ARM64 OrbStack Docker host. The only source change made during verification is
the release-smoke fix that stages HookScript's Composer dependencies outside
the Gateway source tree. Nothing in this record was pushed, published, or
deployed.

## Target

The production target is `infra/docker/Gateway.router.Dockerfile`, as selected
by `.agents/skills/gateway-frankenphp-build/SKILL.md`. It builds the
FrankenPHP Go-library Router with a matching ZTS/embed PHP toolchain and these
tags:

```text
nobrotli,nowatcher,nomercure
```

The earlier native `wtr/watcher-c.h` failure did not represent this production
target because the release build intentionally excludes FrankenPHP's file
watcher.

## Commands and results

The optional Caddy integration compiled and its module tests passed:

```bash
make test-frankenphp
```

Router packaging checks passed:

```bash
bash scripts/test-router-image-packaging
```

The canonical production image built successfully:

```bash
docker build --progress=plain \
  --build-arg GATEWAY_ROUTER_RUNTIME=frankenphp \
  -f infra/docker/Gateway.router.Dockerfile \
  -t gateway-router:frankenphp-verify-061497a2 .
```

Local image identity:

```text
tag:          gateway-router:frankenphp-verify-061497a2
image ID:     sha256:d7f77a282e41d3fa8efc23ec4c8d95c4c4dbdcb79c88073ba6aee14ee8a11f4d
architecture: arm64
size:         371461954 bytes (about 354.3 MiB)
```

The release-image companion smoke passed against that exact image without
rebuilding it:

```bash
GATEWAY_ROUTER_SKIP_BUILD=true \
GATEWAY_ROUTER_IMAGE=gateway-router:frankenphp-verify-061497a2 \
  bash scripts/test-router-companion-image
```

The smoke verified:

- Router health after production-like startup;
- a combined HookScript package built after the image already existed;
- dynamic provider activation through Planner's `/extensions` API;
- warmed PHP companion execution through the private Planner contract;
- deactivation and reactivation changing the advertised provider;
- package tampering failing closed during integrity verification.

The first run exposed a release-smoke defect before container startup:
Composer cannot mirror the root `webong/gateway` path package into a vendor
directory nested inside that same source tree. The smoke now creates a
disposable external Composer workspace, rewrites only its copied path metadata,
and supplies that isolated vendor tree to the HookScript package builder. A
second clean run passed.

## Focused regressions

The focused Go suites passed:

```bash
GOWORK=off GOCACHE=/private/tmp/gateway-go-cache GOTELEMETRY=off \
  go -C pkg/spinner test -mod=mod ./...
GOWORK=off GOCACHE=/private/tmp/gateway-go-cache GOTELEMETRY=off \
  go -C pkg/spinner/cmd/gateway test -mod=mod ./...
GOWORK=off GOCACHE=/private/tmp/gateway-go-cache GOTELEMETRY=off \
  go -C pkg/hookscript test -mod=mod ./...
```

The focused Planner companion suite passed 34 tests with 183 assertions:

```bash
php vendor/bin/pest \
  tests/Feature/CompanionExtensionActivationTest.php \
  tests/Feature/ExtCompanionProcessTest.php \
  tests/Feature/CompanionActionTest.php \
  tests/Feature/ExtensionArtifactReconciliationTest.php --compact
```

## State separation

- Source verification: passed locally.
- Production image build: passed locally.
- Release-image smoke: passed locally.
- Git commit: the smoke fix and this record are committed together.
- GitHub push: not performed.
- Docker registry publication: not performed.
- Deployment: not performed.
