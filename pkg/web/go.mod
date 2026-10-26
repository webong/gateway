module github.com/webong/gateway/pkg/web

go 1.26.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0
	github.com/webong/gateway/arranger v0.0.0
	golang.org/x/net v0.61.0
)

require golang.org/x/text v0.43.0 // indirect

replace github.com/webong/gateway/arranger => ../../mod/arranger
