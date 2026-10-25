module github.com/webong/gateway/pkg/web

go 1.26.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/webong/gateway/arranger v0.0.0
)

replace github.com/webong/gateway/arranger => ../../mod/arranger
