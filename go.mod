module github.com/webong/gateway/pkg/javascript

go 1.24.0

require (
	github.com/buke/quickjs-go v0.7.7
	github.com/webong/gateway/driver v0.0.0
)

replace github.com/webong/gateway/driver => ../../mod/driver
