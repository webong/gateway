package caddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	bridge "github.com/webong/gateway/cmd/bridge"
	smtpgateway "github.com/webong/gateway/cmd/bridge/smtp"
	relayconfig "github.com/webong/gateway/cmd/internal/config"
	"github.com/webong/gateway/cmd/internal/forwarding"
	"github.com/webong/gateway/cmd/internal/logging"
	"github.com/webong/gateway/cmd/internal/workers"
	"go.uber.org/zap"
)

const (
	defaultSMTPListen          = ":2525"
	defaultSMTPPlannerURL      = "http://127.0.0.1:8081"
	defaultSMTPHostname        = "gateway.local"
	defaultSMTPMaxMessageSize  = 10 * 1024 * 1024
	defaultSMTPMaxLineSize     = 16 * 1024
	defaultSMTPMaxRecipients   = 100
	defaultSMTPReadTimeout     = 5 * time.Minute
	defaultSMTPWriteTimeout    = 30 * time.Second
	defaultSMTPPlannerTimeout  = 30 * time.Second
	defaultSMTPMaxWorkers      = 100
	defaultSMTPMaxQueueSize    = 1000
	defaultSMTPMaxIdleConns    = 100
	defaultSMTPMaxConnsPerHost = 100
	defaultSMTPIdleConnTimeout = 90 * time.Second
	defaultSMTPRequestTimeout  = 30 * time.Second
	defaultSMTPLogLevel        = "info"
)

func init() {
	caddy.RegisterModule((*SMTPApp)(nil))
	httpcaddyfile.RegisterGlobalOption("gateway_smtp", parseSMTPApp)
}

// SMTPApp is a Caddy app module which starts the same Go SMTP adapter as
// cmd/proxy. It is a sibling TCP service, not an HTTP handler in php_server.
type SMTPApp struct {
	Listen          string         `json:"listen,omitempty"`
	PlannerURL      string         `json:"planner_url,omitempty"`
	InternalToken   string         `json:"internal_token,omitempty"`
	Hostname        string         `json:"hostname,omitempty"`
	MaxMessageSize  int64          `json:"max_message_size,omitempty"`
	MaxLineSize     int            `json:"max_line_size,omitempty"`
	MaxRecipients   int            `json:"max_recipients,omitempty"`
	ReadTimeout     caddy.Duration `json:"read_timeout,omitempty"`
	WriteTimeout    caddy.Duration `json:"write_timeout,omitempty"`
	PlannerTimeout  caddy.Duration `json:"planner_timeout,omitempty"`
	MaxWorkers      int            `json:"max_workers,omitempty"`
	MaxQueueSize    int            `json:"max_queue_size,omitempty"`
	RequestTimeout  caddy.Duration `json:"request_timeout,omitempty"`
	MaxBodySize     int64          `json:"max_body_size,omitempty"`
	MaxIdleConns    int            `json:"max_idle_conns,omitempty"`
	MaxConnsPerHost int            `json:"max_conns_per_host,omitempty"`
	IdleConnTimeout caddy.Duration `json:"idle_conn_timeout,omitempty"`
	LogLevel        string         `json:"log_level,omitempty"`

	server     *smtpgateway.Server
	forwarder  *forwarding.Forwarder
	workerPool *workers.WorkerPool
}

func (*SMTPApp) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID:  "gateway.smtp",
		New: func() caddy.Module { return new(SMTPApp) },
	}
}

func (a *SMTPApp) Provision(_ caddy.Context) error {
	a.applyDefaults()
	if err := a.Validate(); err != nil {
		return err
	}

	planner, err := bridge.NewHTTPProtocolPlanner(
		a.PlannerURL,
		&http.Client{Timeout: time.Duration(a.RequestTimeout)},
		a.MaxBodySize,
		a.InternalToken,
	)
	if err != nil {
		return err
	}

	settings := &relayconfig.Config{
		MaxBodySize:     a.MaxBodySize,
		MaxWorkers:      a.MaxWorkers,
		MaxQueueSize:    a.MaxQueueSize,
		RequestTimeout:  time.Duration(a.RequestTimeout),
		MaxIdleConns:    a.MaxIdleConns,
		MaxConnsPerHost: a.MaxConnsPerHost,
		IdleConnTimeout: time.Duration(a.IdleConnTimeout),
	}
	logger := logging.NewLogger(a.LogLevel)
	a.forwarder = forwarding.NewForwarder(settings, logger)
	a.workerPool = workers.NewWorkerPool(a.MaxWorkers, a.MaxQueueSize, a.forwarder, logger)
	a.server, err = smtpgateway.NewServer(smtpgateway.Config{
		Address:        a.Listen,
		Hostname:       a.Hostname,
		MaxMessageSize: a.MaxMessageSize,
		MaxLineSize:    a.MaxLineSize,
		MaxRecipients:  a.MaxRecipients,
		ReadTimeout:    time.Duration(a.ReadTimeout),
		WriteTimeout:   time.Duration(a.WriteTimeout),
		PlannerTimeout: time.Duration(a.PlannerTimeout),
	}, planner, bridge.NewRelayExecutor(a.forwarder, a.workerPool))
	if err != nil {
		a.workerPool.Shutdown()
		a.forwarder.CloseIdleConnections()
		return err
	}

	return nil
}

func (a *SMTPApp) Validate() error {
	if strings.TrimSpace(a.Listen) == "" {
		return fmt.Errorf("gateway.smtp listen is required")
	}
	if strings.TrimSpace(a.PlannerURL) == "" {
		return fmt.Errorf("gateway.smtp planner_url is required")
	}
	if strings.TrimSpace(a.InternalToken) == "" {
		return fmt.Errorf("gateway.smtp internal_token is required")
	}
	if a.MaxMessageSize < 1 || a.MaxLineSize < 1 || a.MaxRecipients < 1 {
		return fmt.Errorf("gateway.smtp message and recipient limits must be positive")
	}
	if a.MaxWorkers < 1 || a.MaxQueueSize < 1 || a.MaxIdleConns < 1 || a.MaxConnsPerHost < 1 {
		return fmt.Errorf("gateway.smtp worker and connection limits must be positive")
	}
	if a.ReadTimeout < 1 || a.WriteTimeout < 1 || a.PlannerTimeout < 1 || a.RequestTimeout < 1 || a.IdleConnTimeout < 1 {
		return fmt.Errorf("gateway.smtp timeouts must be positive")
	}

	return nil
}

func (a *SMTPApp) Start() error {
	address, err := caddy.ParseNetworkAddress(a.Listen)
	if err != nil {
		return fmt.Errorf("parse gateway.smtp listen address: %w", err)
	}
	listenerValue, err := address.Listen(context.Background(), 0, net.ListenConfig{})
	if err != nil {
		return fmt.Errorf("listen for gateway.smtp: %w", err)
	}
	listener, ok := listenerValue.(net.Listener)
	if !ok {
		if closer, ok := listenerValue.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		return fmt.Errorf("gateway.smtp listen address does not provide a stream listener")
	}

	go func() {
		if err := a.server.Serve(listener); err != nil && !errors.Is(err, smtpgateway.ErrServerClosed) {
			caddy.Log().Error("gateway.smtp listener stopped", zap.Error(err))
		}
	}()

	return nil
}

func (a *SMTPApp) Stop() error {
	var shutdownErr error
	if a.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		shutdownErr = a.server.Shutdown(ctx)
		cancel()
	}
	if a.workerPool != nil {
		a.workerPool.Shutdown()
	}
	if a.forwarder != nil {
		a.forwarder.CloseIdleConnections()
	}

	return shutdownErr
}

func (a *SMTPApp) applyDefaults() {
	if a.Listen == "" {
		a.Listen = defaultSMTPListen
	}
	if a.PlannerURL == "" {
		a.PlannerURL = defaultSMTPPlannerURL
	}
	if a.Hostname == "" {
		a.Hostname = defaultSMTPHostname
	}
	if a.MaxMessageSize == 0 {
		a.MaxMessageSize = defaultSMTPMaxMessageSize
	}
	if a.MaxLineSize == 0 {
		a.MaxLineSize = defaultSMTPMaxLineSize
	}
	if a.MaxRecipients == 0 {
		a.MaxRecipients = defaultSMTPMaxRecipients
	}
	if a.ReadTimeout == 0 {
		a.ReadTimeout = caddy.Duration(defaultSMTPReadTimeout)
	}
	if a.WriteTimeout == 0 {
		a.WriteTimeout = caddy.Duration(defaultSMTPWriteTimeout)
	}
	if a.PlannerTimeout == 0 {
		a.PlannerTimeout = caddy.Duration(defaultSMTPPlannerTimeout)
	}
	if a.MaxWorkers == 0 {
		a.MaxWorkers = defaultSMTPMaxWorkers
	}
	if a.MaxQueueSize == 0 {
		a.MaxQueueSize = defaultSMTPMaxQueueSize
	}
	if a.RequestTimeout == 0 {
		a.RequestTimeout = caddy.Duration(defaultSMTPRequestTimeout)
	}
	if a.MaxBodySize == 0 {
		a.MaxBodySize = defaultCaddyMaxBodySize
	}
	if a.MaxIdleConns == 0 {
		a.MaxIdleConns = defaultSMTPMaxIdleConns
	}
	if a.MaxConnsPerHost == 0 {
		a.MaxConnsPerHost = defaultSMTPMaxConnsPerHost
	}
	if a.IdleConnTimeout == 0 {
		a.IdleConnTimeout = caddy.Duration(defaultSMTPIdleConnTimeout)
	}
	if a.LogLevel == "" {
		a.LogLevel = defaultSMTPLogLevel
	}
}

func parseSMTPApp(d *caddyfile.Dispenser, _ any) (any, error) {
	var app SMTPApp
	d.Next()
	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "listen":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.Listen = value
		case "planner_url":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.PlannerURL = value
		case "internal_token":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.InternalToken = value
		case "hostname":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.Hostname = value
		case "max_message_size":
			value, err := nextInt64(d)
			if err != nil {
				return nil, err
			}
			app.MaxMessageSize = value
		case "max_line_size", "max_recipients", "max_workers", "max_queue_size", "max_idle_conns", "max_conns_per_host":
			option := d.Val()
			value, err := nextInt(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "max_line_size":
				app.MaxLineSize = value
			case "max_recipients":
				app.MaxRecipients = value
			case "max_workers":
				app.MaxWorkers = value
			case "max_queue_size":
				app.MaxQueueSize = value
			case "max_idle_conns":
				app.MaxIdleConns = value
			case "max_conns_per_host":
				app.MaxConnsPerHost = value
			}
		case "read_timeout", "write_timeout", "planner_timeout", "request_timeout", "idle_conn_timeout":
			option := d.Val()
			value, err := nextDuration(d)
			if err != nil {
				return nil, err
			}
			switch option {
			case "read_timeout":
				app.ReadTimeout = caddy.Duration(value)
			case "write_timeout":
				app.WriteTimeout = caddy.Duration(value)
			case "planner_timeout":
				app.PlannerTimeout = caddy.Duration(value)
			case "request_timeout":
				app.RequestTimeout = caddy.Duration(value)
			case "idle_conn_timeout":
				app.IdleConnTimeout = caddy.Duration(value)
			}
		case "max_body_size":
			value, err := nextInt64(d)
			if err != nil {
				return nil, err
			}
			app.MaxBodySize = value
		case "log_level":
			value, err := stringArg(d)
			if err != nil {
				return nil, err
			}
			app.LogLevel = value
		default:
			return nil, d.Errf("unrecognized gateway_smtp option %q", d.Val())
		}
	}

	data, err := json.Marshal(app)
	if err != nil {
		return nil, err
	}

	return httpcaddyfile.App{Name: "gateway.smtp", Value: data}, nil
}

func stringArg(d *caddyfile.Dispenser) (string, error) {
	if !d.NextArg() {
		return "", d.ArgErr()
	}
	value := d.Val()
	if d.NextArg() {
		return "", d.ArgErr()
	}
	return value, nil
}

var (
	_ caddy.App         = (*SMTPApp)(nil)
	_ caddy.Provisioner = (*SMTPApp)(nil)
	_ caddy.Validator   = (*SMTPApp)(nil)
)
