# Gateway Router

`github.com/webong/gateway/router` assembles the runtime-neutral Spinner with
an injected in-process Planner. Bundled host commands depend on this module and
supply their own PHP adapter; the module contains no RoadRunner or FrankenPHP
runtime dependency.

The development checkout lives in `lib/router`; consuming modules use an
explicit local replacement so the public import path does not expose the
repository's source layout.

Test the module independently from the repository root:

```sh
go -C lib/router test ./...
```
