// Module github.com/webong/gateway/tools/spinner holds cross-module tests that
// compose the Spinner host with real guest engines.
//
// It is intentionally separate from pkg/spinner so that Spinner's own module
// never depends on a concrete engine. Keep this the only kind of module that
// imports both Spinner and guests; the host binary at pkg/spinner/cmd/gateway
// is the other composition root.
module github.com/webong/gateway/tools/spinner

go 1.24.0

require (
	github.com/webong/gateway/driver v0.0.0
	github.com/webong/gateway/pkg/javascript v0.0.0
	github.com/webong/gateway/pkg/lua v0.0.0
	github.com/webong/gateway/pkg/shell v0.0.0
	github.com/webong/gateway/pkg/spinner v0.0.0
)

replace github.com/webong/gateway/driver => ../../mod/driver

replace github.com/webong/gateway/pkg/javascript => ../../pkg/javascript

replace github.com/webong/gateway/pkg/lua => ../../pkg/lua

replace github.com/webong/gateway/pkg/shell => ../../pkg/shell

replace github.com/webong/gateway/pkg/spinner => ../../pkg/spinner
