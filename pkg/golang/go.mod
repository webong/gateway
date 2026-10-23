module github.com/webong/gateway/pkg/golang

go 1.24.0

require (
	github.com/traefik/yaegi v0.16.1
	github.com/webong/gateway/driver v0.0.0
)

replace github.com/webong/gateway/driver => ../../mod/driver
