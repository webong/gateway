# Gateway Lua extension

This installable Gateway Extension adds the `lua` automation language using
gopher-lua. Its Go guest implements the public
`github.com/webong/gateway/arranger/automation` interface and does not import
Spinner. No Gateway host binary links the Lua interpreter: Spinner runs the
engine as a supervised process from a verified `ext.package/v1` package.

## Install it

Release builds already attach this package for Linux amd64 and arm64 as
`webong-lua-<version>-linux-<arch>.tar.gz` with a `.sha256` sidecar. To build it
yourself, run both commands on the Spinner's platform:

```bash
scripts/build-gateway-automation-extension \
  pkg/lua /tmp/extensions/webong%2flua 1.0.0
scripts/archive-gateway-extension \
  /tmp/extensions/webong%2flua /tmp/webong-lua-1.0.0.tar.gz
```

The archive command prints the SHA-256 that Planner pins. Publish the archive
over HTTPS, then activate it through Planner's `/extensions` resource:

```http
POST /extensions
Authorization: Bearer <registry token>
Content-Type: application/json

{
  "extension": "webong/lua",
  "enabled": true,
  "version": "1.0.0",
  "artifact": {
    "url": "https://extensions.example.com/webong-lua-1.0.0.tar.gz",
    "sha256": "<sha256-of-the-tar-gz>"
  }
}
```

Spinner pulls the archive into its `GATEWAY_PLUGIN_DIR` cache, and Planner
pulls it into `GATEWAY_COMPANION_PLUGIN_DIR` to read the language and
capabilities the package declares; both variables must name a writable
directory. A route plan that names `lua` fails closed until Spinner has
installed the package. See [Gateway Automations](../../docs/automations.md) for
the full flow and [Planner extension distribution](../../docs/planner-extensions.md#github-release-artifacts)
for release artifact URLs.

`composer require webong/gateway-lua` is optional. It adds a static Planner
adapter for applications that bind extension behavior into Laravel; it uses the
shared PHP interfaces in `mod/adapter` and does not import Planner
implementation classes. Installing only that package never makes the language
runnable, because Spinner advertises only the engines it has installed.

The runtime opens no standard host libraries and exposes only the bounded
Gateway capability table, including declarative `gateway.schedule({...})`.
