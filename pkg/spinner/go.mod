module github.com/webong/gateway/pkg/spinner

go 1.26.0

require github.com/webong/gateway/arranger v0.0.0

replace github.com/webong/gateway/arranger => ../../mod/arranger

require (
	ergo.services/ergo v1.999.330
	github.com/emersion/go-imap/v2 v2.0.0-beta.8
	github.com/emersion/go-msgauth v0.7.0
	github.com/emersion/go-sasl v0.0.0-20241020182733-b788ff22d5a6
	github.com/emersion/go-smtp v0.24.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/jackc/pgx/v4 v4.18.3
	github.com/miekg/dns v1.1.65
	github.com/webong/ext/pkg/plugin-go/contract v0.6.0
	github.com/wttw/spf v0.0.0-20260423095328-b281622f2e8a
	golang.org/x/net v0.60.0
	golang.org/x/sys v0.48.0
	golang.org/x/time v0.11.0
)

replace github.com/webong/ext/pkg/plugin-go => ../../../ext/pkg/plugin-go

replace github.com/webong/ext/pkg/plugin-go/hashicorp => ../../../ext/pkg/plugin-go/hashicorp

require (
	github.com/Masterminds/semver/v3 v3.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/emersion/go-message v0.18.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/jackc/chunkreader/v2 v2.0.1 // indirect
	github.com/jackc/pgconn v1.14.3 // indirect
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgproto3/v2 v2.3.3 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgtype v1.14.0 // indirect
	github.com/lib/pq v1.10.9 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)
