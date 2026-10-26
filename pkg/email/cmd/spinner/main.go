// Command spinner is Email's installable Gateway Extension entrypoint.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	emailruntime "github.com/webong/gateway/pkg/email/spinner"
	"github.com/webong/gateway/pkg/email/spinner/imap"
	"github.com/webong/gateway/pkg/email/spinner/mailnet"
	"github.com/webong/gateway/pkg/email/spinner/pop3"
	"github.com/webong/gateway/pkg/email/spinner/smtp"
)

type configuration struct {
	SMTP *smtpConfiguration `json:"smtp,omitempty"`
	IMAP *imapConfiguration `json:"imap,omitempty"`
	POP3 *pop3Configuration `json:"pop3,omitempty"`
}

type smtpConfiguration struct {
	Address               string `json:"address"`
	Hostname              string `json:"hostname,omitempty"`
	TLSCertFile           string `json:"tls_cert_file,omitempty"`
	TLSKeyFile            string `json:"tls_key_file,omitempty"`
	ImplicitTLS           bool   `json:"implicit_tls,omitempty"`
	AuthEnabled           bool   `json:"auth_enabled,omitempty"`
	RecipientPreflight    bool   `json:"recipient_preflight,omitempty"`
	MaxMessageSize        int64  `json:"max_message_size,omitempty"`
	MaxRecipients         int    `json:"max_recipients,omitempty"`
	MaxConnections        int    `json:"max_connections,omitempty"`
	MessagesPerMinute     int    `json:"messages_per_minute,omitempty"`
	MessageBurst          int    `json:"message_burst,omitempty"`
	ReadTimeoutMS         int    `json:"read_timeout_ms,omitempty"`
	WriteTimeoutMS        int    `json:"write_timeout_ms,omitempty"`
	PlannerTimeoutMS      int    `json:"planner_timeout_ms,omitempty"`
	DNSLookupTimeoutMS    int    `json:"dns_lookup_timeout_ms,omitempty"`
	ClamdNetwork          string `json:"clamd_network,omitempty"`
	ClamdAddress          string `json:"clamd_address,omitempty"`
	ClamdTimeoutMS        int    `json:"clamd_timeout_ms,omitempty"`
	QuarantinePath        string `json:"quarantine_path,omitempty"`
	QuarantineRetentionMS int    `json:"quarantine_retention_ms,omitempty"`
}

type imapConfiguration struct {
	Address           string                   `json:"address"`
	Hostname          string                   `json:"hostname,omitempty"`
	TLSCertFile       string                   `json:"tls_cert_file"`
	TLSKeyFile        string                   `json:"tls_key_file"`
	Upstreams         map[string]imap.Upstream `json:"upstreams,omitempty"`
	MaxConnections    int                      `json:"max_connections,omitempty"`
	AuthPerMinute     int                      `json:"auth_per_minute,omitempty"`
	AuthBurst         int                      `json:"auth_burst,omitempty"`
	IdleTimeoutMS     int                      `json:"idle_timeout_ms,omitempty"`
	PlannerTimeoutMS  int                      `json:"planner_timeout_ms,omitempty"`
	UpstreamTimeoutMS int                      `json:"upstream_timeout_ms,omitempty"`
}

type pop3Configuration struct {
	Address           string                   `json:"address"`
	Hostname          string                   `json:"hostname,omitempty"`
	TLSCertFile       string                   `json:"tls_cert_file"`
	TLSKeyFile        string                   `json:"tls_key_file"`
	Upstreams         map[string]pop3.Upstream `json:"upstreams,omitempty"`
	MaxConnections    int                      `json:"max_connections,omitempty"`
	AuthPerMinute     int                      `json:"auth_per_minute,omitempty"`
	AuthBurst         int                      `json:"auth_burst,omitempty"`
	IdleTimeoutMS     int                      `json:"idle_timeout_ms,omitempty"`
	PlannerTimeoutMS  int                      `json:"planner_timeout_ms,omitempty"`
	UpstreamTimeoutMS int                      `json:"upstream_timeout_ms,omitempty"`
}

type boundListener struct {
	name     string
	listener net.Listener
	serve    func(net.Listener) error
}

func main() {
	if err := runtimecontract.ServeRuntime(context.Background(), runtimecontract.RuntimeStarterFunc(start)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, request runtimecontract.RuntimeStartRequest, caller plugin.ReverseCaller) (runtimecontract.RuntimeStartResponse, error) {
	var configured configuration
	if len(request.Configuration) == 0 {
		return runtimecontract.RuntimeStartResponse{}, errors.New("Email runtime configuration is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(request.Configuration))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("decode Email runtime configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return runtimecontract.RuntimeStartResponse{}, errors.New("Email runtime configuration contains trailing data")
	}
	if configured.SMTP == nil && configured.IMAP == nil && configured.POP3 == nil {
		return runtimecontract.RuntimeStartResponse{}, errors.New("Email requires at least one configured listener")
	}
	host := runtimecontract.NewRuntimeHost(caller, request.AutomationLanguages)
	dnsResolver, err := emailruntime.NewDNSResolver(host.DNS())
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	listeners := make([]boundListener, 0, 3)
	closeListeners := func() {
		for _, listener := range listeners {
			_ = listener.listener.Close()
		}
	}
	if configured.SMTP != nil {
		server, err := newSMTPServer(*configured.SMTP, host, dnsResolver)
		if err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		listener, err := net.Listen("tcp", configured.SMTP.Address)
		if err != nil {
			return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("listen for SMTP: %w", err)
		}
		listeners = append(listeners, boundListener{name: "SMTP", listener: listener, serve: server.Serve})
	}
	if configured.IMAP != nil {
		server, err := newIMAPServer(*configured.IMAP, host, dnsResolver)
		if err != nil {
			closeListeners()
			return runtimecontract.RuntimeStartResponse{}, err
		}
		listener, err := net.Listen("tcp", configured.IMAP.Address)
		if err != nil {
			closeListeners()
			return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("listen for IMAP: %w", err)
		}
		listeners = append(listeners, boundListener{name: "IMAP", listener: listener, serve: server.Serve})
	}
	if configured.POP3 != nil {
		server, err := newPOP3Server(*configured.POP3, host, dnsResolver)
		if err != nil {
			closeListeners()
			return runtimecontract.RuntimeStartResponse{}, err
		}
		listener, err := net.Listen("tcp", configured.POP3.Address)
		if err != nil {
			closeListeners()
			return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("listen for POP3: %w", err)
		}
		listeners = append(listeners, boundListener{name: "POP3", listener: listener, serve: server.Serve})
	}
	for _, bound := range listeners {
		go func(listener boundListener) {
			if err := listener.serve(listener.listener); err != nil {
				fmt.Fprintf(os.Stderr, "%s listener stopped: %v\n", listener.name, err)
			}
		}(bound)
	}
	return runtimecontract.RuntimeStartResponse{}, nil
}

func newSMTPServer(config smtpConfiguration, host *runtimecontract.RuntimeHost, resolver *emailruntime.DNSResolver) (*smtp.Server, error) {
	tlsConfig, err := optionalSMTPTLS(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	var scanner smtp.MalwareScanner
	if config.ClamdAddress != "" {
		network := config.ClamdNetwork
		if network == "" {
			network = "tcp"
		}
		clamdTimeout := durationMS(config.ClamdTimeoutMS)
		if clamdTimeout == 0 {
			clamdTimeout = 10 * time.Second
		}
		scanner, err = smtp.NewClamdScanner(network, config.ClamdAddress, clamdTimeout)
		if err != nil {
			return nil, err
		}
	}
	var quarantine smtp.QuarantineStore
	if config.QuarantinePath != "" {
		quarantine, err = smtp.NewFileQuarantine(config.QuarantinePath)
		if err != nil {
			return nil, err
		}
	}
	dnsTimeout := durationMS(config.DNSLookupTimeoutMS)
	loopKey := make([]byte, 32)
	if _, err := rand.Read(loopKey); err != nil {
		return nil, fmt.Errorf("create SMTP forwarding-loop key: %w", err)
	}
	return smtp.NewServer(smtp.Config{
		Address: config.Address, Hostname: config.Hostname, TLSConfig: tlsConfig,
		ImplicitTLS: config.ImplicitTLS, AuthEnabled: config.AuthEnabled, RecipientPreflight: config.RecipientPreflight,
		MaxMessageSize: config.MaxMessageSize, MaxRecipients: config.MaxRecipients, MaxConnections: config.MaxConnections,
		MessagesPerMinute: config.MessagesPerMinute, MessageBurst: config.MessageBurst,
		ReadTimeout: durationMS(config.ReadTimeoutMS), WriteTimeout: durationMS(config.WriteTimeoutMS), PlannerTimeout: durationMS(config.PlannerTimeoutMS),
		SPFTimeout: dnsTimeout, SPFEvaluator: smtp.NewDNSSPFEvaluator(resolver),
		DKIMTimeout: dnsTimeout, DKIMVerifier: smtp.NewDNSDKIMVerifier(resolver.LookupTXT),
		DomainExistenceTimeout: dnsTimeout, DomainExistenceResolver: resolver,
		Scanner: scanner, ScanTimeout: durationMS(config.ClamdTimeoutMS), Quarantine: quarantine,
		QuarantineRetention: durationMS(config.QuarantineRetentionMS), SubmitTimeout: 30 * time.Second,
		LoopKey: loopKey,
	}, host.ProtocolPlanner(), host.Executor())
}

func newIMAPServer(config imapConfiguration, host *runtimecontract.RuntimeHost, resolver *emailruntime.DNSResolver) (*imap.Server, error) {
	tlsConfig, err := imap.LoadTLSConfig(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	return imap.NewServer(imap.Config{
		Address: config.Address, Hostname: config.Hostname, TLSConfig: tlsConfig, Upstreams: config.Upstreams,
		DialManagedUpstream: func(ctx context.Context, address, serverName, networkScope string, timeout time.Duration, roots *x509.CertPool) (net.Conn, error) {
			return mailnet.DialTLSMailUpstreamWithResolver(ctx, address, serverName, networkScope, timeout, roots, resolver.LookupIPAddr)
		},
		MaxConnections: config.MaxConnections, AuthPerMinute: config.AuthPerMinute, AuthBurst: config.AuthBurst,
		IdleTimeout: durationMS(config.IdleTimeoutMS), PlannerTimeout: durationMS(config.PlannerTimeoutMS), UpstreamTimeout: durationMS(config.UpstreamTimeoutMS),
	}, host.ProtocolPlanner())
}

func newPOP3Server(config pop3Configuration, host *runtimecontract.RuntimeHost, resolver *emailruntime.DNSResolver) (*pop3.Server, error) {
	tlsConfig, err := pop3.LoadTLSConfig(config.TLSCertFile, config.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	return pop3.NewServer(pop3.Config{
		Address: config.Address, Hostname: config.Hostname, TLSConfig: tlsConfig, Upstreams: config.Upstreams,
		DialManagedUpstream: func(ctx context.Context, address, serverName, networkScope string, timeout time.Duration, roots *x509.CertPool) (net.Conn, error) {
			return mailnet.DialTLSMailUpstreamWithResolver(ctx, address, serverName, networkScope, timeout, roots, resolver.LookupIPAddr)
		},
		MaxConnections: config.MaxConnections, AuthPerMinute: config.AuthPerMinute, AuthBurst: config.AuthBurst,
		IdleTimeout: durationMS(config.IdleTimeoutMS), PlannerTimeout: durationMS(config.PlannerTimeoutMS), UpstreamTimeout: durationMS(config.UpstreamTimeoutMS),
	}, host.ProtocolPlanner())
}

func optionalSMTPTLS(certFile, keyFile string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" {
		return nil, nil
	}
	return smtp.LoadTLSConfig(certFile, keyFile)
}

func durationMS(value int) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value) * time.Millisecond
}
