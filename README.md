# Gateway JavaScript extension

This optional Go package adds the `javascript` automation language using the
pinned QuickJS-NG adapter. It implements the public
`github.com/webong/gateway/arranger/automation` interface and does not import
Spinner. A default Gateway build does not link QuickJS.

Enable it when building a Gateway composition root:

```bash
go build -tags gateway_javascript ./cmd/gateway
```

Install the companion Composer package in Planner so it can register the
language as Spinner-executed with route and schedule capabilities:

```bash
composer require webong/gateway-javascript
```

The provider uses the shared PHP interfaces in `lib/adapter`; it does not
import Planner implementation classes. Spinner still advertises the compiled
engine at runtime, so installing only the companion package fails closed.

Use `gateway_javascript,gateway_lua` to install both Go language extensions.
The engine exposes only `gateway.event`, `gateway.get`, `gateway.set`,
`gateway.respond`, `gateway.forward`, and declarative `gateway.schedule`; it does not expose host modules,
filesystem, processes, imports, or raw network access.
