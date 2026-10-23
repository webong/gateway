module github.com/webong/gateway/pkg/reverb

go 1.24.0

require (
	github.com/gorilla/websocket v1.5.3
	github.com/webong/gateway/driver v0.0.0
)

replace github.com/webong/gateway/driver => ../../mod/driver
