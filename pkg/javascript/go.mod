module github.com/webong/gateway/pkg/javascript

go 1.26.0

require (
	github.com/buke/quickjs-go v0.7.7
	github.com/webong/gateway/driver v0.0.0
	github.com/webong/gateway/extension v0.0.0
)

require github.com/webong/ext/pkg/plugin-go/contract v0.6.0 // indirect

replace github.com/webong/gateway/driver => ../../mod/driver

replace github.com/webong/gateway/extension => ../../mod/extension

replace github.com/webong/ext/pkg/plugin-go => ../../../ext/pkg/plugin-go
