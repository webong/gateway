module github.com/webong/gateway/pkg/reverb

go 1.26.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/webong/gateway/arranger v0.0.0
)

require github.com/webong/ext/pkg/plugin-go/contract v0.6.0 // indirect

replace github.com/webong/gateway/arranger => ../../mod/arranger
