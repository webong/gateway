module github.com/webong/gateway/pkg/shell

go 1.24.0

require (
	github.com/webong/gateway/driver v0.0.0
	golang.org/x/crypto v0.38.0
)

require golang.org/x/sys v0.33.0 // indirect

replace github.com/webong/gateway/driver => ../../mod/driver
