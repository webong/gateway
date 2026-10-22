module github.com/webong/gateway/ext/mercure

go 1.24.0

require (
	github.com/webong/gateway v0.0.0
	github.com/webong/gateway/driver v0.0.0
)

replace github.com/webong/gateway => ../..

replace github.com/webong/gateway/driver => ../../lib/driver
