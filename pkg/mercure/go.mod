module github.com/webong/gateway/pkg/mercure

go 1.26.0

require (
	github.com/webong/gateway/arranger v0.0.0
	github.com/webong/gateway/pkg/spinner v0.0.0
)

require (
	github.com/webong/ext/pkg/ext-go/engine v0.3.0 // indirect
	github.com/webong/workload v0.2.1 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/webong/gateway/arranger => ../../mod/arranger

replace github.com/webong/gateway/pkg/spinner => ../../pkg/spinner
