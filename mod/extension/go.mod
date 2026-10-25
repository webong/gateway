module github.com/webong/gateway/extension

go 1.26.0

require (
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0
	github.com/webong/gateway/driver v0.0.0
)

replace github.com/webong/ext/pkg/plugin-go => ../../../ext/pkg/plugin-go

replace github.com/webong/gateway/driver => ../driver
