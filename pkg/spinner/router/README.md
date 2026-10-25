# Gateway Router host

`github.com/webong/gateway/pkg/spinner/router` assembles Spinner with an
injected in-process Planner. Bundled host commands supply their own PHP adapter;
this package contains no RoadRunner or FrankenPHP runtime dependency.

Runtime-neutral public routing and deny-by-default private-control policy live
in `github.com/webong/gateway/arranger/router`. Spinner capabilities contribute
their own narrow private Planner request policies rather than adding product
routes to the shared Router.

Test the host package from the repository root:

```sh
GOWORK=off go -C pkg/spinner test ./router
```
