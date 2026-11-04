# Gateway JavaScript extension

This installable Gateway Extension adds the `javascript` automation language
using the pinned QuickJS-NG adapter. Its Go guest implements the public
`github.com/webong/gateway/arranger/automation` interface and does not import
Spinner. No Gateway host binary links QuickJS: Spinner runs the engine as a
supervised process from a verified `ext.package/v1` package.

## Install it

Release builds already attach this package for Linux amd64 and arm64 as
`webong-javascript-<version>-linux-<arch>.tar.gz` with a `.sha256` sidecar. To
build it yourself, run both commands on the Spinner's platform (the guest uses
cgo, so the build host needs a C toolchain):

```bash
scripts/build-gateway-automation-extension \
  pkg/javascript /tmp/extensions/webong%2fjavascript 1.0.0
scripts/archive-gateway-extension \
  /tmp/extensions/webong%2fjavascript /tmp/webong-javascript-1.0.0.tar.gz
```

The archive command prints the SHA-256 that Planner pins. Publish the archive
over HTTPS, then activate it through Planner's `/extensions` resource:

```http
POST /extensions
Authorization: Bearer <registry token>
Content-Type: application/json

{
  "extension": "webong/javascript",
  "enabled": true,
  "version": "1.0.0",
  "artifact": {
    "url": "https://extensions.example.com/webong-javascript-1.0.0.tar.gz",
    "sha256": "<sha256-of-the-tar-gz>"
  }
}
```

Spinner pulls the archive into its `GATEWAY_PLUGIN_DIR` cache, and Planner
pulls it into `GATEWAY_COMPANION_PLUGIN_DIR` to read the language and
capabilities the package declares; both variables must name a writable
directory. A route plan that names `javascript` fails closed until Spinner has
installed the package. See [Gateway Automations](../../docs/automations.md) for
the full flow and [Planner extension distribution](../../docs/planner-extensions.md#github-release-artifacts)
for release artifact URLs.

`composer require webong/gateway-javascript` is optional. It adds a static
Planner adapter for applications that bind extension behavior into Laravel; it
uses the shared PHP interfaces in `mod/adapter` and does not import Planner
implementation classes. Installing only that package never makes the language
runnable, because Spinner advertises only the engines it has installed.

The engine exposes only `gateway.event`, `gateway.get`, `gateway.set`,
`gateway.respond`, `gateway.forward`, and declarative `gateway.schedule`; it does not expose host modules,
filesystem, processes, imports, or raw network access.
