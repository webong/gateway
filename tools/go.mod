// Module github.com/webong/gateway/tools holds test-only Go code that is
// not part of the Spinner host: disposable fixtures, smoke clients, diagnostics
// and benchmarks. None of it is shipped.
//
// The cross-module Spinner tests live in the nested module
// github.com/webong/gateway/tools/spinner, which is the only tools code allowed
// to depend on both Spinner and concrete guest engines.
module github.com/webong/gateway/tools

go 1.24.0

require (
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6
	github.com/emersion/go-smtp v0.25.0
	github.com/gorilla/websocket v1.5.3
	github.com/miekg/dns v1.1.65
	github.com/wttw/spf v0.0.0-20260423095328-b281622f2e8a
)

require (
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/net v0.35.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
)
