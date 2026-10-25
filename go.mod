module github.com/webong/gateway/pkg/shell

go 1.26.0

require (
	github.com/webong/gateway/driver v0.0.0
	github.com/webong/gateway/extension v0.0.0
	golang.org/x/crypto v0.38.0
)

require (
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
)

replace github.com/webong/gateway/driver => ../../mod/driver

replace github.com/webong/gateway/extension => ../../mod/extension

replace github.com/webong/ext/pkg/plugin-go => ../../../ext/pkg/plugin-go
