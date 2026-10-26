module github.com/webong/gateway/pkg/email

go 1.26.0

require (
	github.com/emersion/go-imap/v2 v2.0.0-beta.8
	github.com/emersion/go-msgauth v0.7.0
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6
	github.com/emersion/go-smtp v0.24.0
	github.com/google/uuid v1.6.0
	github.com/miekg/dns v1.1.62
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0
	github.com/webong/gateway/arranger v0.0.0
	github.com/wttw/spf v0.0.0-20260423095328-b281622f2e8a
	golang.org/x/net v0.60.0
)

require (
	github.com/emersion/go-message v0.18.2 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace github.com/webong/gateway/arranger => ../../mod/arranger
