module github.com/webong/gateway

go 1.26.0

require github.com/miekg/dns v1.1.65

require (
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace github.com/webong/ext/pkg/plugin-go => ../ext/pkg/plugin-go
