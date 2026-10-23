module github.com/webong/gateway/pkg/lua

go 1.24.0

require (
	github.com/webong/gateway/driver v0.0.0
	github.com/yuin/gopher-lua v1.1.1
)

replace github.com/webong/gateway/driver => ../../mod/driver
