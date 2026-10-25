module github.com/webong/gateway/pkg/centrifugo

go 1.26.0

require (
	github.com/webong/gateway/arranger v0.0.0
	github.com/webong/gateway/pkg/spinner v0.0.0
)

require ergo.services/ergo v1.999.330 // indirect

replace github.com/webong/gateway/arranger => ../../mod/arranger

replace github.com/webong/gateway/pkg/spinner => ../../pkg/spinner
