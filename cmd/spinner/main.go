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
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	runtimecontract "github.com/webong/gateway/arranger/integration"
	emailruntime "github.com/webong/gateway/pkg/email/spinner"
	"github.com/webong/gateway/pkg/email/spinner/delivery"
	"github.com/webong/gateway/pkg/email/spinner/deliveryfailure"
	"github.com/webong/gateway/pkg/email/spinner/imap"
	"github.com/webong/gateway/pkg/email/spinner/mailnet"
	"github.com/webong/gateway/pkg/email/spinner/mailpoll"
	"github.com/webong/gateway/pkg/email/spinner/pop3"
	"github.com/webong/gateway/pkg/email/spinner/smtp"
	"github.com/webong/gateway/pkg/email/spinner/smtpout"
)

type configuration struct {
	SMTP                *smtpConfiguration        `json:"smtp,omitempty"`
	IMAP                *imapConfiguration        `json:"imap,omitempty"`
	POP3                *pop3Configuration        `json:"pop3,omitempty"`
	SMTPRelay           *smtpRelayConfiguration   `json:"smtp_relay,omitempty"`
	HostedMail          *hostedMailConfiguration  `json:"hosted_mail,omitempty"`
	MailboxPoll         *mailboxPollConfiguration `json:"mailbox_poll,omitempty"`
	FailureNoticeDomain string                    `json:"failure_notice_domain,omitempty"`
}

type mailboxPollConfiguration struct {
	PrivateCallbackCIDRs []string `json:"private_callback_cidrs,omitempty"`
	CallbackTimeoutMS    int      `json:"callback_timeout_ms,omitempty"`
	UpstreamTimeoutMS    int      `json:"upstream_timeout_ms,omitempty"`
}

type smtpRelayConfiguration struct {
	Address       string `json:"address"`
	LocalName     string `json:"local_name,omitempty"`
	ServerName    string `json:"server_name,omitempty"`
	TLSMode       string `json:"tls_mode"`
	TimeoutMS     int    `json:"timeout_ms,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

type hostedMailConfiguration struct {
	AllowedHosts []string `json:"allowed_hosts"`
}

type runtime struct {
	mu       sync.RWMutex
	adapters map[string]runtimecontract.GatewayDeliveryAdapter
	jobs     map[string]func(context.Context, string, map[string]any) error
	failures map[string]runtimecontract.DeliveryFailureHandler
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
	runtime := &runtime{}
	if err := runtimecontract.ServeRuntimeWithHandler(context.Background(), runtimecontract.RuntimeStarterFunc(runtime.start), runtimecontract.RuntimeOperationHandlerFunc(runtime.handle)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func start(_ context.Context, request runtimecontract.RuntimeStartRequest, caller plugin.ReverseCaller) (runtimecontract.RuntimeStartResponse, error) {
	return new(runtime).start(context.Background(), request, caller)
}

func (runtime *runtime) start(_ context.Context, request runtimecontract.RuntimeStartRequest, caller plugin.ReverseCaller) (runtimecontract.RuntimeStartResponse, error) {
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
	adapters := make(map[string]runtimecontract.GatewayDeliveryAdapter)
	host := runtimecontract.NewRuntimeHost(caller, request.AutomationLanguages)
	privatePlanner := host.PrivatePlanner()
	signer, err := smtpout.NewPlannerDKIMSigner(privatePlanner)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure Email DKIM resolver: %w", err)
	}
	managed, err := delivery.NewPlannerManagedSMTPAdapter(privatePlanner, 30*time.Second, signer)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure managed SMTP adapter: %w", err)
	}
	adapters["managed.smtp"] = managed
	mailboxResolver, err := smtpout.NewPlannerMailboxResolver(privatePlanner)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure mailbox SMTP resolver: %w", err)
	}
	mailbox, err := smtpout.NewManagedMailboxAdapter(mailboxResolver, 30*time.Second)
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure mailbox SMTP adapter: %w", err)
	}
	adapters["mailbox.smtp"] = mailbox
	if configured.HostedMail != nil {
		hostedResolver, resolverErr := delivery.NewPlannerHostedMailResolver(privatePlanner)
		if resolverErr != nil {
			return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure hosted mail resolver: %w", resolverErr)
		}
		hosted, hostedErr := delivery.NewManagedHostedMailAdapter(hostedResolver, configured.HostedMail.AllowedHosts, nil)
		if hostedErr != nil {
			return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure hosted mail adapter: %w", hostedErr)
		}
		adapters["hosted.mail"] = hosted
	}
	if configured.SMTPRelay != nil {
		relay := smtpout.Config{
			Address: configured.SMTPRelay.Address, LocalName: configured.SMTPRelay.LocalName,
			ServerName: configured.SMTPRelay.ServerName, TLSMode: smtpout.TLSMode(configured.SMTPRelay.TLSMode),
			Timeout: durationMS(configured.SMTPRelay.TimeoutMS),
		}
		if configured.SMTPRelay.CredentialRef != "" {
			adapter, credentialErr := smtpout.NewSecretGatewayAdapter(relay, configured.SMTPRelay.CredentialRef, host.Secrets(), signer)
			if credentialErr != nil {
				return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure credentialed Email SMTP relay: %w", credentialErr)
			}
			adapters[string(runtimecontract.ProtocolSMTP)] = adapter
		} else {
			sender, senderErr := smtpout.NewSender(relay)
			if senderErr != nil {
				return runtimecontract.RuntimeStartResponse{}, fmt.Errorf("configure Email SMTP relay: %w", senderErr)
			}
			adapter, adapterErr := smtpout.NewGatewayAdapter(sender, signer)
			if adapterErr != nil {
				return runtimecontract.RuntimeStartResponse{}, adapterErr
			}
			adapters[string(runtimecontract.ProtocolSMTP)] = adapter
		}
	}
	jobs := make(map[string]func(context.Context, string, map[string]any) error)
	failures := make(map[string]runtimecontract.DeliveryFailureHandler)
	response := runtimecontract.RuntimeStartResponse{DeliveryAdapters: adapterNames(adapters)}
	if configured.FailureNoticeDomain != "" {
		handler, err := deliveryfailure.New(configured.FailureNoticeDomain)
		if err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		for _, adapter := range []string{"smtp", "managed.smtp", "mailbox.smtp", "hosted.mail"} {
			failures[adapter] = handler
		}
		response.DeliveryFailureHandlers = failureHandlerNames(failures)
	}
	if configured.SMTP == nil && configured.IMAP == nil && configured.POP3 == nil && configured.MailboxPoll == nil {
		runtime.mu.Lock()
		runtime.adapters, runtime.jobs, runtime.failures = adapters, jobs, failures
		runtime.mu.Unlock()
		if err := startEmailHTTP(&response, host, request, len(adapters) > 0, configured.FailureNoticeDomain != ""); err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		return response, nil
	}
	dnsResolver, err := emailruntime.NewDNSResolver(host.DNS())
	if err != nil {
		return runtimecontract.RuntimeStartResponse{}, err
	}
	if configured.MailboxPoll != nil {
		control, err := mailpoll.NewExtensionControlPlane(host.PrivatePlanner())
		if err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		callback, err := newMailboxCallback(dnsResolver, configured.MailboxPoll.PrivateCallbackCIDRs, durationMS(configured.MailboxPoll.CallbackTimeoutMS))
		if err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		upstreamTimeout := durationMS(configured.MailboxPoll.UpstreamTimeoutMS)
		if upstreamTimeout == 0 {
			upstreamTimeout = 10 * time.Second
		}
		runner := mailpoll.Runner{
			Control: control,
			Dial: func(ctx context.Context, claim mailpoll.Claim) (net.Conn, error) {
				return mailnet.DialTLSMailUpstreamWithResolver(ctx, claim.UpstreamAddress, claim.UpstreamServerName,
					claim.UpstreamNetworkScope, upstreamTimeout, nil, dnsResolver.LookupIPAddr)
			},
			Deliver: callback.Deliver,
		}
		jobs["native.imap"] = func(ctx context.Context, source string, payload map[string]any) error {
			switch payload["action"] {
			case nil, "poll":
				return runner.Run(ctx, source)
			case "baseline":
				return runner.Baseline(ctx, source)
			default:
				return fmt.Errorf("unsupported IMAP schedule action")
			}
		}
		response.Jobs = []runtimecontract.RuntimeJob{{Name: "imap", Language: "native.imap"}}
	}
	runtime.mu.Lock()
	runtime.adapters, runtime.jobs, runtime.failures = adapters, jobs, failures
	runtime.mu.Unlock()
	if configured.SMTP == nil && configured.IMAP == nil && configured.POP3 == nil {
		if err := startEmailHTTP(&response, host, request, len(adapters) > 0, configured.FailureNoticeDomain != ""); err != nil {
			return runtimecontract.RuntimeStartResponse{}, err
		}
		return response, nil
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
	if err := startEmailHTTP(&response, host, request, len(adapters) > 0, configured.FailureNoticeDomain != ""); err != nil {
		closeListeners()
		return runtimecontract.RuntimeStartResponse{}, err
	}
	return response, nil
}

func startEmailHTTP(response *runtimecontract.RuntimeStartResponse, host *runtimecontract.RuntimeHost,
	request runtimecontract.RuntimeStartRequest, privateSubmission, failureNotices bool,
) error {
	publicListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen for public Email routes: %w", err)
	}
	publicServer := &http.Server{
		Handler:           emailruntime.SubmissionHandler(http.NotFoundHandler(), host.PassThrough(), request.MaxBodySize),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	go serveHTTP("public Email routes", publicServer, publicListener)
	response.HTTP = []runtimecontract.HTTPMount{{
		Prefix: emailruntime.SubmissionPath, Address: "http://" + publicListener.Addr().String(),
	}}
	if !privateSubmission {
		return nil
	}
	privateHandler, err := emailruntime.NewAdminSubmissionHandler(
		host.Executor(), nil, nil, request.MaxBodySize, request.InstanceKey, failureNotices,
	)
	if err != nil {
		_ = publicListener.Close()
		return err
	}
	privateListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = publicListener.Close()
		return fmt.Errorf("listen for private Email routes: %w", err)
	}
	privateServer := &http.Server{Handler: privateHandler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	go serveHTTP("private Email routes", privateServer, privateListener)
	response.PrivateHTTP = []runtimecontract.HTTPMount{{
		Prefix: emailruntime.SubmissionPath, Address: "http://" + privateListener.Addr().String(),
	}}
	return nil
}

func serveHTTP(name string, server *http.Server, listener net.Listener) {
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "%s stopped: %v\n", name, err)
	}
}

func (runtime *runtime) handle(ctx context.Context, request plugin.Request) (json.RawMessage, error) {
	if request.Contract == runtimecontract.DeliveryFailureContract && request.Operation == runtimecontract.OperationDeliveryFailure {
		var input runtimecontract.DeliveryFailureRequest
		if err := plugin.Decode(request.Payload, &input); err != nil || input.ID == "" || input.Delivery.Validate() != nil {
			return nil, plugin.ErrInvalid
		}
		runtime.mu.RLock()
		handler := runtime.failures[input.Delivery.AdapterName()]
		runtime.mu.RUnlock()
		if handler == nil {
			return nil, plugin.ErrUnsupported
		}
		followUp, err := handler.FollowUp(ctx, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(runtimecontract.DeliveryFailureResponse{FollowUp: followUp})
	}
	if request.Contract == runtimecontract.JobContract && request.Operation == runtimecontract.OperationRunJob {
		var input runtimecontract.JobRequest
		if err := plugin.Decode(request.Payload, &input); err != nil || input.Name == "" || input.Source == "" {
			return nil, plugin.ErrInvalid
		}
		language := "native." + input.Name
		runtime.mu.RLock()
		job := runtime.jobs[language]
		runtime.mu.RUnlock()
		if job == nil {
			return nil, plugin.ErrUnsupported
		}
		if err := job(ctx, input.Source, input.Payload); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}
	if request.Contract != runtimecontract.DeliveryAdapterContract || request.Operation != runtimecontract.OperationDeliverAdapter {
		return nil, plugin.ErrUnsupported
	}
	var input runtimecontract.DeliveryAdapterRequest
	if err := plugin.Decode(request.Payload, &input); err != nil || input.Delivery.Validate() != nil || input.Adapter != input.Delivery.AdapterName() {
		return nil, plugin.ErrInvalid
	}
	runtime.mu.RLock()
	adapter := runtime.adapters[input.Adapter]
	runtime.mu.RUnlock()
	if adapter == nil {
		return nil, plugin.ErrUnsupported
	}
	response := runtimecontract.DeliveryAdapterResponse{}
	var err error
	if reporting, ok := adapter.(runtimecontract.GatewayDeliveryResultAdapter); ok {
		response.Result, err = reporting.DeliverGatewayResult(ctx, input.Delivery)
	} else {
		err = adapter.DeliverGateway(ctx, input.Delivery)
	}
	if err != nil {
		switch {
		case errors.Is(err, runtimecontract.ErrPermanentDelivery):
			response.Failure = runtimecontract.AdapterFailurePermanent
		case errors.Is(err, runtimecontract.ErrUncertainDelivery):
			response.Failure = runtimecontract.AdapterFailureUncertain
		default:
			response.Failure = runtimecontract.AdapterFailureTransient
		}
	}
	return json.Marshal(response)
}

func adapterNames(adapters map[string]runtimecontract.GatewayDeliveryAdapter) []string {
	names := make([]string, 0, len(adapters))
	for name := range adapters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func failureHandlerNames(handlers map[string]runtimecontract.DeliveryFailureHandler) []string {
	names := make([]string, 0, len(handlers))
	for name := range handlers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
