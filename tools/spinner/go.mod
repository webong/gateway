// Module github.com/webong/gateway/tools/spinner holds cross-module tests that
// compose the Spinner host with real guest engines.
//
// It is intentionally separate from pkg/spinner so that Spinner's own module
// never depends on a concrete engine. Keep this the only kind of module that
// imports both Spinner and guests; the host binary at pkg/spinner/cmd/gateway
// is the other composition root.
module github.com/webong/gateway/tools/spinner

go 1.26.0

require (
	github.com/emersion/go-smtp v0.24.0
	github.com/miekg/dns v1.1.65
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0
	github.com/webong/gateway/arranger v0.0.0
	github.com/webong/gateway/pkg/javascript v0.0.0
	github.com/webong/gateway/pkg/lua v0.0.0
	github.com/webong/gateway/pkg/shell v0.0.0
	github.com/webong/gateway/pkg/spinner v0.0.0
	github.com/webong/gateway/pkg/web v0.0.0
)

require (
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
)

require (
	ergo.services/ergo v1.999.330 // indirect
	github.com/buke/quickjs-go v0.7.7 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/yuin/gopher-lua v1.1.1 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/webong/gateway/arranger => ../../mod/arranger

replace github.com/webong/gateway/pkg/javascript => ../../pkg/javascript

replace github.com/webong/gateway/pkg/lua => ../../pkg/lua

replace github.com/webong/gateway/pkg/shell => ../../pkg/shell

replace github.com/webong/gateway/pkg/spinner => ../../pkg/spinner

replace github.com/webong/gateway/pkg/email => ../../pkg/email

replace github.com/webong/gateway/pkg/web => ../../pkg/web

replace github.com/webong/gateway/pkg/dns => ../../pkg/dns
