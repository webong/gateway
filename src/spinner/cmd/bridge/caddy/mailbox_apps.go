package caddy

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/imap"
	"github.com/webong/gateway/src/spinner/cmd/bridge/pop3"
	"go.uber.org/zap"
)

func init() {
	caddy.RegisterModule((*POP3App)(nil))
	caddy.RegisterModule((*IMAPApp)(nil))
	httpcaddyfile.RegisterGlobalOption("gateway_pop3", parsePOP3App)
	httpcaddyfile.RegisterGlobalOption("gateway_imap", parseIMAPApp)
}

type mailboxServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
}

type MailboxUpstream struct {
	Address    string `json:"address"`
	ServerName string `json:"server_name"`
}

// MailboxAppOptions are shared by the POP3 and IMAP Caddy apps. The listener
// is TLS-only; upstream IDs and addresses are operator configuration, never
// values supplied by a mailbox client or Planner response.
type MailboxAppOptions struct {
	Listen          string                     `json:"listen,omitempty"`
	PlannerURL      string                     `json:"planner_url,omitempty"`
	InternalToken   string                     `json:"internal_token,omitempty"`
	Hostname        string                     `json:"hostname,omitempty"`
	TLSCertFile     string                     `json:"tls_cert_file,omitempty"`
	TLSKeyFile      string                     `json:"tls_key_file,omitempty"`
	UpstreamCAFile  string                     `json:"upstream_ca_file,omitempty"`
	Upstreams       map[string]MailboxUpstream `json:"upstreams,omitempty"`
	MaxConnections  int                        `json:"max_connections,omitempty"`
	AuthPerMinute   int                        `json:"auth_per_minute,omitempty"`
	AuthBurst       int                        `json:"auth_burst,omitempty"`
	IdleTimeout     caddy.Duration             `json:"idle_timeout,omitempty"`
	PlannerTimeout  caddy.Duration             `json:"planner_timeout,omitempty"`
	UpstreamTimeout caddy.Duration             `json:"upstream_timeout,omitempty"`
	MaxBodySize     int64                      `json:"max_body_size,omitempty"`

	server   mailboxServer
	listener net.Listener
}

type POP3App struct{ MailboxAppOptions }
type IMAPApp struct{ MailboxAppOptions }

func (*POP3App) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "gateway.pop3", New: func() caddy.Module { return new(POP3App) }}
}

func (*IMAPApp) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "gateway.imap", New: func() caddy.Module { return new(IMAPApp) }}
}

func (app *POP3App) Provision(_ caddy.Context) error { return app.MailboxAppOptions.provision("pop3") }
func (app *IMAPApp) Provision(_ caddy.Context) error { return app.MailboxAppOptions.provision("imap") }
func (app *POP3App) Start() error                    { return app.MailboxAppOptions.start("pop3") }
func (app *IMAPApp) Start() error                    { return app.MailboxAppOptions.start("imap") }
func (app *POP3App) Stop() error                     { return app.MailboxAppOptions.stop() }
func (app *IMAPApp) Stop() error                     { return app.MailboxAppOptions.stop() }

func (app *MailboxAppOptions) provision(kind string) error {
	app.applyDefaults(kind)
	if app.InternalToken == "" || app.PlannerURL == "" || app.Listen == "" || app.TLSCertFile == "" || app.TLSKeyFile == "" || len(app.Upstreams) == 0 {
		return fmt.Errorf("gateway.%s requires a listener, private Planner URL, internal token, TLS certificate and registered upstream", kind)
	}
	if app.MaxConnections < 1 || app.AuthPerMinute < 1 || app.AuthBurst < 1 || app.IdleTimeout < caddy.Duration(10*time.Minute) || app.PlannerTimeout < 1 || app.UpstreamTimeout < 1 || app.MaxBodySize < 1 {
		return fmt.Errorf("gateway.%s connection, authentication or timeout limits are invalid", kind)
	}
	planner, err := bridge.NewHTTPProtocolPlanner(app.PlannerURL, &http.Client{Timeout: time.Duration(app.PlannerTimeout)}, app.MaxBodySize, app.InternalToken)
	if err != nil {
		return err
	}
	var upstreamRoots *x509.CertPool
	if app.UpstreamCAFile != "" {
		certificate, err := os.ReadFile(app.UpstreamCAFile)
		if err != nil {
			return fmt.Errorf("read gateway.%s upstream CA: %w", kind, err)
		}
		upstreamRoots = x509.NewCertPool()
		if !upstreamRoots.AppendCertsFromPEM(certificate) {
			return fmt.Errorf("gateway.%s upstream CA file has no valid certificates", kind)
		}
	}
	if kind == "pop3" {
		tlsConfig, err := pop3.LoadTLSConfig(app.TLSCertFile, app.TLSKeyFile)
		if err != nil {
			return err
		}
		upstreams := make(map[string]pop3.Upstream, len(app.Upstreams))
		for id, upstream := range app.Upstreams {
			upstreams[id] = pop3.Upstream{Address: upstream.Address, ServerName: upstream.ServerName}
		}
		app.server, err = pop3.NewServer(pop3.Config{
			Address: app.Listen, Hostname: app.Hostname, TLSConfig: tlsConfig, Upstreams: upstreams, UpstreamRootCAs: upstreamRoots,
			MaxConnections: app.MaxConnections, AuthPerMinute: app.AuthPerMinute, AuthBurst: app.AuthBurst,
			IdleTimeout: time.Duration(app.IdleTimeout), PlannerTimeout: time.Duration(app.PlannerTimeout), UpstreamTimeout: time.Duration(app.UpstreamTimeout),
		}, planner)
		return err
	}
	tlsConfig, err := imap.LoadTLSConfig(app.TLSCertFile, app.TLSKeyFile)
	if err != nil {
		return err
	}
	upstreams := make(map[string]imap.Upstream, len(app.Upstreams))
	for id, upstream := range app.Upstreams {
		upstreams[id] = imap.Upstream{Address: upstream.Address, ServerName: upstream.ServerName}
	}
	app.server, err = imap.NewServer(imap.Config{
		Address: app.Listen, Hostname: app.Hostname, TLSConfig: tlsConfig, Upstreams: upstreams, UpstreamRootCAs: upstreamRoots,
		MaxConnections: app.MaxConnections, AuthPerMinute: app.AuthPerMinute, AuthBurst: app.AuthBurst,
		IdleTimeout: time.Duration(app.IdleTimeout), PlannerTimeout: time.Duration(app.PlannerTimeout), UpstreamTimeout: time.Duration(app.UpstreamTimeout),
	}, planner)
	return err
}

func (app *MailboxAppOptions) applyDefaults(kind string) {
	if app.Listen == "" {
		if kind == "pop3" {
			app.Listen = ":995"
		} else {
			app.Listen = ":993"
		}
	}
	if app.Hostname == "" {
		app.Hostname = "gateway.local"
	}
	if app.MaxConnections == 0 {
		app.MaxConnections = 1000
	}
	if app.AuthPerMinute == 0 {
		app.AuthPerMinute = 30
	}
	if app.AuthBurst == 0 {
		app.AuthBurst = 5
	}
	if app.IdleTimeout == 0 {
		app.IdleTimeout = caddy.Duration(10 * time.Minute)
		if kind == "imap" {
			app.IdleTimeout = caddy.Duration(30 * time.Minute)
		}
	}
	if app.PlannerTimeout == 0 {
		app.PlannerTimeout = caddy.Duration(10 * time.Second)
	}
	if app.UpstreamTimeout == 0 {
		app.UpstreamTimeout = caddy.Duration(10 * time.Second)
	}
	if app.MaxBodySize == 0 {
		app.MaxBodySize = defaultCaddyMaxBodySize
	}
}

func (app *MailboxAppOptions) start(kind string) error {
	if app.server == nil {
		return fmt.Errorf("gateway.%s is not provisioned", kind)
	}
	address, err := caddy.ParseNetworkAddress(app.Listen)
	if err != nil {
		return fmt.Errorf("parse gateway.%s listen address: %w", kind, err)
	}
	listenerValue, err := address.Listen(context.Background(), 0, net.ListenConfig{})
	if err != nil {
		return fmt.Errorf("listen for gateway.%s: %w", kind, err)
	}
	listener, ok := listenerValue.(net.Listener)
	if !ok {
		if closer, ok := listenerValue.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return fmt.Errorf("gateway.%s listen address does not provide a stream listener", kind)
	}
	app.listener = listener
	go func() {
		if err := app.server.Serve(listener); err != nil && !errors.Is(err, pop3.ErrServerClosed) && !errors.Is(err, imap.ErrServerClosed) {
			caddy.Log().Error("gateway."+kind+" listener stopped", zap.Error(err))
		}
	}()
	return nil
}

func (app *MailboxAppOptions) stop() error {
	if app.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return app.server.Shutdown(ctx)
}

func parsePOP3App(d *caddyfile.Dispenser, _ any) (any, error) {
	return parseMailboxApp(d, "gateway.pop3")
}

func parseIMAPApp(d *caddyfile.Dispenser, _ any) (any, error) {
	return parseMailboxApp(d, "gateway.imap")
}

func parseMailboxApp(d *caddyfile.Dispenser, kind string) (any, error) {
	app := MailboxAppOptions{Upstreams: make(map[string]MailboxUpstream)}
	d.Next()
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "listen", "planner_url", "internal_token", "hostname", "tls_cert_file", "tls_key_file", "upstream_ca_file":
			option := d.Val()
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "listen":
				app.Listen = value
			case "planner_url":
				app.PlannerURL = value
			case "internal_token":
				app.InternalToken = value
			case "hostname":
				app.Hostname = value
			case "tls_cert_file":
				app.TLSCertFile = value
			case "tls_key_file":
				app.TLSKeyFile = value
			case "upstream_ca_file":
				app.UpstreamCAFile = value
			}
		case "upstream":
			args := d.RemainingArgs()
			if len(args) != 3 || args[0] == "" || args[1] == "" || args[2] == "" {
				return nil, d.ArgErr()
			}
			if _, exists := app.Upstreams[args[0]]; exists {
				return nil, d.Errf("duplicate %s upstream %q", kind, args[0])
			}
			app.Upstreams[args[0]] = MailboxUpstream{Address: args[1], ServerName: args[2]}
		case "max_connections", "auth_per_minute", "auth_burst":
			option := d.Val()
			value, err := nextInt(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "max_connections":
				app.MaxConnections = value
			case "auth_per_minute":
				app.AuthPerMinute = value
			case "auth_burst":
				app.AuthBurst = value
			}
		case "idle_timeout", "planner_timeout", "upstream_timeout":
			option := d.Val()
			value, err := nextDuration(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "idle_timeout":
				app.IdleTimeout = caddy.Duration(value)
			case "planner_timeout":
				app.PlannerTimeout = caddy.Duration(value)
			case "upstream_timeout":
				app.UpstreamTimeout = caddy.Duration(value)
			}
		case "max_body_size":
			value, err := nextInt64(d)
			if err != nil {
				return nil, err
			}
			app.MaxBodySize = value
		default:
			return nil, d.Errf("unrecognized %s option %q", kind, d.Val())
		}
	}
	data, err := json.Marshal(app)
	if err != nil {
		return nil, err
	}
	return httpcaddyfile.App{Name: kind, Value: data}, nil
}

var (
	_ caddy.App         = (*POP3App)(nil)
	_ caddy.Provisioner = (*POP3App)(nil)
	_ caddy.App         = (*IMAPApp)(nil)
	_ caddy.Provisioner = (*IMAPApp)(nil)
)
