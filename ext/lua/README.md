# Gateway Lua extension

This optional Go package adds the `lua` automation language using gopher-lua.
It implements the public `github.com/webong/gateway/driver/automation`
interface and does not import Spinner. A default Gateway build does not link
the Lua interpreter.

Enable it when building a Gateway composition root:

```bash
go build -tags gateway_lua ./src/spinner/cmd/proxy
```

Install the companion Composer package in Planner so it can register the
language as Spinner-executed with route and schedule capabilities:

```bash
composer require webong/gateway-lua
```

The provider uses the shared PHP interfaces in `lib/contract`; it does not
import Planner implementation classes. Spinner still advertises the compiled
engine at runtime, so installing only the companion package fails closed.

Use `gateway_javascript,gateway_lua` to install both Go language extensions.
The runtime opens no standard host libraries and exposes only the bounded
Gateway capability table, including declarative `gateway.schedule({...})`.
