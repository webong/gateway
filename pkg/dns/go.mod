module github.com/webong/gateway/pkg/dns

go 1.26.0

require (
	github.com/miekg/dns v1.1.65
	github.com/webong/gateway/arranger v0.0.0
	golang.org/x/time v0.11.0
)

require (
	golang.org/x/mod v0.23.0 // indirect
	golang.org/x/net v0.35.0 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/tools v0.30.0 // indirect
)

replace github.com/webong/gateway/arranger => ../../mod/arranger
