module github.com/webong/gateway/pkg/lua

go 1.26.0

require (
	github.com/webong/gateway/arranger v0.0.0
	github.com/yuin/gopher-lua v1.1.1
)

require github.com/webong/ext/pkg/plugin-go/contract v0.6.0 // indirect

replace github.com/webong/gateway/arranger => ../../mod/arranger

replace github.com/webong/ext/pkg/plugin-go => ../../../ext/pkg/plugin-go
