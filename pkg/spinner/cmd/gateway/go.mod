module github.com/webong/gateway/pkg/spinner/cmd/gateway

go 1.26.0

require github.com/webong/gateway/pkg/spinner v0.0.0

require (
	ergo.services/ergo v1.999.330 // indirect
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	github.com/emersion/go-imap/v2 v2.0.0-beta.8 // indirect
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/emersion/go-msgauth v0.7.0 // indirect
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6 // indirect
	github.com/emersion/go-smtp v0.24.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgconn v1.14.3 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.3.3 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgtype v1.14.0 // indirect
	github.com/jackc/pgx/v4 v4.18.3 // indirect
	github.com/miekg/dns v1.1.65 // indirect
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0 // indirect
	github.com/webong/gateway/arranger v0.0.0 // indirect
	github.com/webong/gateway/pkg/email v0.0.0 // indirect
	github.com/wttw/spf v0.0.0-20260423095328-b281622f2e8a // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
)

replace github.com/webong/gateway/arranger => ../../../../mod/arranger

replace github.com/webong/gateway/pkg/spinner => ../..

replace github.com/webong/gateway/pkg/email => ../../../email

replace github.com/webong/ext/pkg/plugin-go => ../../../../../ext/pkg/plugin-go

replace github.com/webong/ext/pkg/plugin-go/hashicorp => ../../../../../ext/pkg/plugin-go/hashicorp
