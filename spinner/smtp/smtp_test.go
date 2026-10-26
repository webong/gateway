package smtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	smtpclient "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/arranger/integration"
)

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

type spfEvaluatorFunc func(context.Context, net.IP, string, string) SPFEvidence

type signingDomainCheckerFunc func(string) error

func (f signingDomainCheckerFunc) CheckSigningDomain(domain string) error { return f(domain) }

func (f spfEvaluatorFunc) Evaluate(ctx context.Context, ip net.IP, from, helo string) SPFEvidence {
	return f(ctx, ip, from, helo)
}

func (f plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return f(ctx, event)
}

func TestRecipientPreflightRejectsBeforeAcceptingRecipient(t *testing.T) {
	var events []bridge.GatewayEvent
	s := &session{
		config: Config{PlannerTimeout: time.Second, MaxRecipients: 10, RecipientPreflight: true, Hostname: "smtp.example.test"},
		planner: plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			events = append(events, event)
			decision := bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept}
			if event.Route == "unknown@example.test" {
				decision.Action, decision.StatusCode, decision.Message = bridge.GatewayReject, 550, "unknown recipient"
			}
			return decision, nil
		}),
		from: "sender@example.test", sessionID: "session-1",
	}
	if err := s.Rcpt("valid@example.test", nil); err != nil {
		t.Fatal(err)
	}
	err := s.Rcpt("unknown@example.test", nil)
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 550 {
		t.Fatalf("expected recipient-time 550, got %v", err)
	}
	if len(s.recipients) != 1 || s.recipients[0] != "valid@example.test" {
		t.Fatalf("rejected recipient was accepted: %v", s.recipients)
	}
	if len(events) != 2 || events[1].Kind != bridge.EventRecipient || events[1].Route != "unknown@example.test" || events[1].Attributes["accepted_recipients"] != `["valid@example.test"]` {
		t.Fatalf("unexpected recipient planning events: %+v", events)
	}
}

func TestRecipientPreflightFailsClosedOnInvalidDecision(t *testing.T) {
	s := &session{
		config: Config{PlannerTimeout: time.Second, MaxRecipients: 10, RecipientPreflight: true},
		planner: plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
				Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: "https://example.test"}}}, nil
		}),
	}
	var smtpErr *smtpclient.SMTPError
	if err := s.Rcpt("valid@example.test", nil); !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatalf("expected temporary failure for unsupported recipient decision, got %v", err)
	}
	if len(s.recipients) != 0 {
		t.Fatalf("recipient was accepted after invalid decision: %v", s.recipients)
	}
}

type recordingExecutor struct {
	mu         sync.Mutex
	deliveries []bridge.GatewayDelivery
}

func (e *recordingExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.deliveries = append(e.deliveries, delivery)
	return true
}

func (e *recordingExecutor) DurableGatewayDeliveries() bool { return true }

type volatileExecutor struct{ recordingExecutor }

func (e *volatileExecutor) DurableGatewayDeliveries() bool { return false }

type batchRecordingExecutor struct {
	recordingExecutor
	batches [][]bridge.GatewayDelivery
	accept  bool
}

func (e *batchRecordingExecutor) EnqueueGatewayBatch(_ string, deliveries []bridge.GatewayDelivery) bool {
	e.batches = append(e.batches, deliveries)
	return e.accept
}

func TestSMTPAcknowledgesMultipleDestinationsOnlyAfterOneDurableBatch(t *testing.T) {
	for _, accepted := range []bool{true, false} {
		executor := &batchRecordingExecutor{accept: accepted}
		s := &session{executor: executor, config: Config{MaxMessageSize: 1024, PlannerTimeout: time.Second},
			from: "sender@example.test", recipients: []string{"inbox@example.test"},
			planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP,
					Action: bridge.GatewayDeliver, Deliveries: []bridge.GatewayDelivery{
						{Protocol: bridge.ProtocolHTTP, Target: "https://one.example.test/mail"},
						{Protocol: bridge.ProtocolHTTP, Target: "https://two.example.test/mail"},
					}}, nil
			})}
		err := s.Data(strings.NewReader("Subject: test\r\n\r\nbody\r\n"))
		if accepted && err != nil {
			t.Fatal(err)
		}
		if !accepted {
			var smtpErr *smtpclient.SMTPError
			if !errors.As(err, &smtpErr) || smtpErr.Code != 452 {
				t.Fatalf("failed batch was acknowledged: %v", err)
			}
		}
		if len(executor.batches) != 1 || len(executor.batches[0]) != 2 || len(executor.snapshot()) != 0 {
			t.Fatalf("SMTP enqueued individual destinations before batch acceptance: %+v, %+v", executor.batches, executor.snapshot())
		}
	}
}

func (e *recordingExecutor) snapshot() []bridge.GatewayDelivery {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]bridge.GatewayDelivery(nil), e.deliveries...)
}

func TestServerUsesGoSMTPForTransactionsAndQueuesGatewayDelivery(t *testing.T) {
	executor := &recordingExecutor{}
	var planned bridge.GatewayEvent
	server, err := NewServer(Config{
		Address:        "127.0.0.1:0",
		Hostname:       "smtp.example.test",
		PlannerTimeout: time.Second,
		SPFEvaluator: spfEvaluatorFunc(func(_ context.Context, ip net.IP, from, helo string) SPFEvidence {
			if ip.String() != "127.0.0.1" || from != "sender@example.test" || helo != "client.example.test" {
				t.Errorf("wrong SPF inputs: peer=%s from=%s helo=%s", ip, from, helo)
			}
			return SPFEvidence{Result: "fail", Identity: "mailfrom", Domain: "example.test"}
		}),
	}, plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		planned = event
		return bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolSMTP,
			Action:   bridge.GatewayDeliver,
			Deliveries: []bridge.GatewayDelivery{{
				Protocol: bridge.ProtocolHTTP,
				Target:   "https://subscriber.example.test/mail",
			}},
		}, nil
	}), executor)
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ErrServerClosed) {
			t.Errorf("shutdown SMTP server: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve SMTP server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not stop")
		}
	})

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Hello("client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("recipient@example.test", nil); err != nil {
		t.Fatal(err)
	}

	data, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	message := "Subject: hello\r\n\r\nmessage body\r\n"
	if _, err := io.WriteString(data, message); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Quit(); err != nil {
		t.Fatal(err)
	}

	if planned.Protocol != bridge.ProtocolSMTP || planned.Kind != bridge.EventTransaction || planned.Route != "recipient@example.test" {
		t.Fatalf("unexpected planned event: %+v", planned)
	}
	if planned.Attributes["mail_from"] != "sender@example.test" || string(planned.Payload) != message {
		t.Fatalf("unexpected SMTP event data: %+v", planned)
	}
	if planned.Attributes["remote_ip"] != "127.0.0.1" {
		t.Fatalf("expected trusted SMTP peer IP, got %+v", planned.Attributes)
	}
	if planned.Attributes["spf_result"] != "fail" || planned.Attributes["spf_identity"] != "mailfrom" || planned.Attributes["spf_domain"] != "example.test" {
		t.Fatalf("expected SPF evidence even when it fails, got %+v", planned.Attributes)
	}
	if planned.Attributes["dkim_result"] != "none" {
		t.Fatalf("expected unsigned DKIM evidence, got %+v", planned.Attributes)
	}
	deliveries := executor.snapshot()
	if len(deliveries) != 1 || string(deliveries[0].Payload) != message {
		t.Fatalf("expected queued message delivery: %+v", deliveries)
	}
}

func TestSMTPDoesNotAcknowledgeVolatileDelivery(t *testing.T) {
	executor := &volatileExecutor{}
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{
				Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
				Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: "https://subscriber.example.test/mail"}},
			}, nil
		}),
		executor: executor,
		config:   Config{MaxMessageSize: 1024, PlannerTimeout: time.Second},
		from:     "sender@example.test", recipients: []string{"recipient@example.test"},
	}
	err := s.Data(strings.NewReader("Subject: test\r\n\r\nbody\r\n"))
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatalf("volatile delivery was acknowledged: %v", err)
	}
	if len(executor.snapshot()) != 0 {
		t.Fatal("volatile executor was called")
	}
}

func TestSMTPQueuesReplyDeliveryBeforeAcknowledging(t *testing.T) {
	executor := &recordingExecutor{}
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{
				Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
				Reply: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://subscriber.example.test/mail"},
			}, nil
		}),
		executor: executor,
		config:   Config{MaxMessageSize: 1024, PlannerTimeout: time.Second},
		from:     "sender@example.test", recipients: []string{"recipient@example.test"},
	}
	if err := s.Data(strings.NewReader("Subject: test\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	if deliveries := executor.snapshot(); len(deliveries) != 1 || deliveries[0].Target != "https://subscriber.example.test/mail" {
		t.Fatalf("SMTP reply delivery was dropped: %+v", deliveries)
	}
}

func TestSMTPSubmitAcknowledgesOnlyAfterRelayAccepts(t *testing.T) {
	var accepted bridge.GatewayDelivery
	delivery := bridge.GatewayDelivery{
		Protocol: bridge.ProtocolSMTP, Target: "first@example.net",
		Attributes: map[string]string{"mail_from": "sender@example.test", "recipients": `["first@example.net","second@example.net"]`},
	}
	makeSession := func(submitter bridge.GatewayDeliveryAdapter) *session {
		return &session{
			planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{
					Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP,
					Action: bridge.GatewaySubmit, Deliveries: []bridge.GatewayDelivery{delivery},
				}, nil
			}),
			executor: &recordingExecutor{},
			config:   Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, SubmitTimeout: time.Second, Submitter: submitter, LoopKey: []byte("test-loop-key")},
			from:     "sender@example.test", recipients: []string{"first@example.test", "second@example.test"},
		}
	}
	acceptedByRelay := bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, value bridge.GatewayDelivery) error {
		accepted = value
		return nil
	})
	if err := makeSession(acceptedByRelay).Data(strings.NewReader("Subject: test\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	if accepted.Target != delivery.Target || !strings.HasSuffix(string(accepted.Payload), "Subject: test\r\n\r\nbody\r\n") || forwardingHops(accepted.Payload, []byte("test-loop-key")) != 1 {
		t.Fatalf("relay did not receive the whole message: %+v", accepted)
	}
	failedRelay := bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
		return errors.New("relay refused DATA")
	})
	err := makeSession(failedRelay).Data(strings.NewReader("Subject: test\r\n\r\nbody\r\n"))
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatalf("unaccepted relay handoff was acknowledged: %v", err)
	}
}

func TestSMTPSubmitUsesDurableHostQueueWithoutAnInProcessAdapter(t *testing.T) {
	executor := &recordingExecutor{}
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{
				Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewaySubmit,
				Deliveries: []bridge.GatewayDelivery{{
					Protocol: bridge.ProtocolSMTP, Target: "inbox@example.net",
					Attributes: map[string]string{"mail_from": "sender@example.test", "recipients": `["inbox@example.net"]`},
				}},
			}, nil
		}),
		executor: executor,
		config: Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, SubmitTimeout: time.Second,
			LoopKey: []byte("process-owned-loop-key")},
		from: "sender@example.test", recipients: []string{"inbox@example.net"},
	}
	if err := s.Data(strings.NewReader("Subject: queued submit\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	queued := executor.snapshot()
	if len(queued) != 1 || queued[0].AdapterName() != "smtp" || forwardingHops(queued[0].Payload, []byte("process-owned-loop-key")) != 1 {
		t.Fatalf("SMTP submission was not handed to the durable host queue: %+v", queued)
	}
}

func TestManagedSMTPSubmitAcknowledgesOnlyAfterDurableQueueAccepts(t *testing.T) {
	executor := &recordingExecutor{}
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP,
				Action: bridge.GatewaySubmit, Deliveries: []bridge.GatewayDelivery{{
					Protocol: bridge.ProtocolSMTP, Adapter: "managed.smtp", Target: "buyer@example.net",
					Attributes: map[string]string{"mail_from": "support@operator.test"},
				}}}, nil
		}),
		executor: executor,
		config: Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, SubmitTimeout: time.Second,
			LoopKey: []byte("test-loop-key")},
		from: "support@operator.test", recipients: []string{"buyer@example.net"},
	}
	if err := s.Data(strings.NewReader("From: support@operator.test\r\nReply-To: conversation@reply.operator.test\r\n\r\nhello")); err != nil {
		t.Fatal(err)
	}
	queued := executor.snapshot()
	if len(queued) != 1 || queued[0].AdapterName() != "managed.smtp" ||
		!bytes.Contains(queued[0].Payload, []byte("Reply-To: conversation@reply.operator.test")) {
		t.Fatalf("managed SMTP submission was not durably queued: %+v", queued)
	}
}

func TestAuthenticatedSMTPSubmitMarksOnlyAuthorizedAuthorForSigning(t *testing.T) {
	var submitted bridge.GatewayDelivery
	s := &session{
		planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{
				Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewaySubmit,
				Deliveries: []bridge.GatewayDelivery{{
					Protocol: bridge.ProtocolSMTP, Target: "recipient@example.net",
					Attributes: map[string]string{"mail_from": "sender@example.test", bridge.DKIMSigningDomainAttribute: "forged.example"},
				}},
			}, nil
		}),
		executor: &recordingExecutor{},
		config: Config{AuthEnabled: true, MaxMessageSize: 1024, PlannerTimeout: time.Second, SubmitTimeout: time.Second,
			Submitter: bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
				submitted = delivery
				return nil
			}), LoopKey: []byte("test-loop-key")},
		authenticated: true, authorizedSenders: map[string]struct{}{"sender@example.test": {}},
		from: "sender@example.test", recipients: []string{"recipient@example.net"},
	}
	if err := s.Data(strings.NewReader("From: Sender <sender@example.test>\r\nSubject: test\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	if submitted.Attributes[bridge.DKIMSigningDomainAttribute] != "example.test" {
		t.Fatalf("unexpected DKIM signing domain: %+v", submitted.Attributes)
	}
}

func TestAuthenticatedSMTPDefersMissingDKIMKeyBeforeOutboundHandoff(t *testing.T) {
	for _, action := range []bridge.GatewayAction{bridge.GatewaySubmit, bridge.GatewayDeliver} {
		t.Run(string(action), func(t *testing.T) {
			executor := &recordingExecutor{}
			submitted := false
			s := &session{
				planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
					return bridge.GatewayDecision{
						Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: action,
						Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolSMTP, Target: "recipient@example.net"}},
					}, nil
				}),
				executor: executor,
				config: Config{AuthEnabled: true, MaxMessageSize: 1024, PlannerTimeout: time.Second,
					SubmitTimeout: time.Second,
					Submitter: bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
						submitted = true
						return nil
					}),
					SigningDomainChecker: signingDomainCheckerFunc(func(domain string) error {
						if domain != "example.test" {
							t.Fatalf("unexpected signing domain: %s", domain)
						}
						return errors.New("missing signing key")
					}),
				},
				authenticated: true, authorizedSenders: map[string]struct{}{"sender@example.test": {}},
				from: "sender@example.test", recipients: []string{"recipient@example.net"},
			}
			var smtpErr *smtpclient.SMTPError
			if err := s.Data(strings.NewReader("From: sender@example.test\r\nSubject: test\r\n\r\nbody\r\n")); !errors.As(err, &smtpErr) || smtpErr.Code != 451 || submitted || len(executor.snapshot()) != 0 {
				t.Fatalf("missing signing key was acknowledged or handed off: err=%v submitted=%v", err, submitted)
			}
		})
	}
}

func TestServerUsesGoSMTPMessageLimit(t *testing.T) {
	server, err := NewServer(Config{
		Address:        "127.0.0.1:0",
		MaxMessageSize: 4,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("planner should not run for an oversized message")
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-serveErr
	})

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Hello("client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("recipient@example.test", nil); err != nil {
		t.Fatal(err)
	}
	data, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(data, "12345\r\n"); err != nil {
		t.Fatal(err)
	}
	err = data.Close()
	var smtpErr *smtpclient.SMTPError
	if !errors.As(err, &smtpErr) || smtpErr.Code != 552 {
		t.Fatalf("expected SMTP 552 data error, got %v", err)
	}
}

func TestServerRequiresTLSForSMTPAuth(t *testing.T) {
	_, err := NewServer(Config{
		Address:     "127.0.0.1:0",
		AuthEnabled: true,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err == nil || err.Error() != "SMTP AUTH requires TLS configuration" {
		t.Fatalf("expected SMTP AUTH TLS requirement, got %v", err)
	}
}

func TestLoadTLSConfigRequiresCertificateAndKeyTogether(t *testing.T) {
	_, err := LoadTLSConfig("certificate.pem", "")
	if err == nil {
		t.Fatal("expected incomplete SMTP TLS configuration to fail")
	}
}

func TestSMTPAuthDelegatesCredentialsToProtocolPlanner(t *testing.T) {
	var planned bridge.GatewayEvent
	session := &session{
		planner: plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			planned = event
			return bridge.GatewayDecision{
				Version:  bridge.ProtocolVersion,
				Protocol: bridge.ProtocolSMTP,
				Action:   bridge.GatewayAccept,
				Metadata: map[string]string{"auth_identity": "account-123", "authorized_mail_from": `["sender@example.test"]`, "submission_daily_limit": "2", "submission_daily_byte_limit": "64"},
			}, nil
		}),
		config: Config{
			AuthEnabled:    true,
			TLSConfig:      &tls.Config{},
			PlannerTimeout: time.Second,
			Hostname:       "smtp.example.test",
		},
		sessionID: "smtp-test-session",
	}

	client := sasl.NewPlainClient("", "subscriber", "secret")
	mechanism, initialResponse, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := session.Auth(mechanism)
	if err != nil {
		t.Fatal(err)
	}
	_, done, err := authenticator.Next(initialResponse)
	if err != nil || !done {
		t.Fatalf("expected successful SMTP authentication, done=%v err=%v", done, err)
	}
	if planned.Kind != bridge.EventAuthenticate || planned.Attributes["username"] != "subscriber" || planned.Attributes["password"] != "secret" {
		t.Fatalf("unexpected authentication event: %+v", planned)
	}
	if !session.authenticated || session.authUsername != "subscriber" || session.authIdentity != "account-123" {
		t.Fatalf("expected authenticated session identity, got authenticated=%v username=%q identity=%q", session.authenticated, session.authUsername, session.authIdentity)
	}
	if got := session.transactionAttributes()["auth_username"]; got != "subscriber" {
		t.Fatalf("expected transaction to carry authenticated username, got %q", got)
	}
	if got := session.transactionAttributes()["auth_identity"]; got != "account-123" {
		t.Fatalf("expected transaction to carry canonical identity, got %q", got)
	}
	if got := session.transactionAttributes()["submission_daily_limit"]; got != "2" {
		t.Fatalf("expected transaction to carry approved quota, got %q", got)
	}
	if got := session.transactionAttributes()["submission_daily_byte_limit"]; got != "64" {
		t.Fatalf("expected transaction to carry approved byte quota, got %q", got)
	}
	if err := session.Mail("sender@example.test", nil); err != nil {
		t.Fatalf("expected authorized sender to be accepted: %v", err)
	}
	if err := session.Mail("other@example.test", nil); err == nil {
		t.Fatal("expected unauthorized envelope sender to be rejected")
	}
	if session.from != "" || len(session.recipients) != 0 {
		t.Fatal("rejected MAIL retained an earlier transaction")
	}
	if err := session.Logout(); err != nil {
		t.Fatal(err)
	}
	if session.authenticated || session.authUsername != "" || session.authIdentity != "" || session.submissionDailyLimit != "" || session.submissionDailyByteLimit != "" {
		t.Fatalf("expected logout to clear authenticated identity, got authenticated=%v username=%q identity=%q", session.authenticated, session.authUsername, session.authIdentity)
	}
	if session.authorizedSenders != nil {
		t.Fatal("logout retained authorized sender addresses")
	}
	if _, ok := session.transactionAttributes()["auth_username"]; ok {
		t.Fatal("transaction retained authenticated username after logout")
	}
}

func TestSMTPAuthRequiresExplicitValidSenderGrant(t *testing.T) {
	for _, test := range []struct {
		name  string
		grant string
	}{
		{name: "missing"},
		{name: "empty", grant: `[]`},
		{name: "wildcard", grant: `["*@example.test"]`},
		{name: "display name", grant: `["Sender <sender@example.test>"]`},
		{name: "null sender", grant: `[""]`},
		{name: "malformed", grant: `not-json`},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := &session{
				planner: plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
					return bridge.GatewayDecision{
						Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept,
						Metadata: map[string]string{"auth_identity": "account-123", "authorized_mail_from": test.grant},
					}, nil
				}),
				config: Config{AuthEnabled: true, PlannerTimeout: time.Second},
			}
			if err := session.authenticate("", "subscriber", "secret"); err == nil {
				t.Fatal("expected invalid sender grant to reject AUTH")
			}
			if session.authenticated || len(session.authorizedSenders) != 0 {
				t.Fatal("invalid sender grant retained authenticated state")
			}
		})
	}
}

func TestSMTPAuthRequiresCanonicalIdentity(t *testing.T) {
	for _, identity := range []string{"", " account-123", "account-123\n", strings.Repeat("x", 256)} {
		s := &session{
			planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{
					Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept,
					Metadata: map[string]string{"auth_identity": identity, "authorized_mail_from": `["sender@example.test"]`},
				}, nil
			}),
			config: Config{AuthEnabled: true, PlannerTimeout: time.Second},
		}
		if err := s.authenticate("", "subscriber", "secret"); err == nil || s.authenticated || s.authIdentity != "" {
			t.Fatalf("invalid canonical identity %q was accepted", identity)
		}
	}
}

func TestSMTPAuthRejectsInvalidSubmissionLimit(t *testing.T) {
	for _, limit := range []string{"0", "-1", "1.5", "1000001", "not-a-number"} {
		s := &session{
			planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{
					Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept,
					Metadata: map[string]string{"auth_identity": "account-123", "authorized_mail_from": `["sender@example.test"]`, "submission_daily_limit": limit},
				}, nil
			}),
			config: Config{AuthEnabled: true, PlannerTimeout: time.Second},
		}
		if err := s.authenticate("", "subscriber", "secret"); err == nil || s.authenticated {
			t.Fatalf("invalid submission limit %q was accepted", limit)
		}
	}
}

func TestSMTPAuthRejectsInvalidSubmissionByteLimit(t *testing.T) {
	for _, limit := range []string{"0", "-1", "1.5", "1000000000001", "not-a-number"} {
		s := &session{
			planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{
					Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept,
					Metadata: map[string]string{"auth_identity": "account-123", "authorized_mail_from": `["sender@example.test"]`, "submission_daily_byte_limit": limit},
				}, nil
			}),
			config: Config{AuthEnabled: true, PlannerTimeout: time.Second},
		}
		if err := s.authenticate("", "subscriber", "secret"); err == nil || s.authenticated {
			t.Fatalf("invalid submission byte limit %q was accepted", limit)
		}
	}
}

func TestAuthenticatedSMTPRejectsUnapprovedMessageAuthor(t *testing.T) {
	session := &session{
		config:        Config{AuthEnabled: true, MaxMessageSize: 1024},
		authenticated: true, authorizedSenders: map[string]struct{}{"sender@example.test": {}},
		from: "sender@example.test", recipients: []string{"recipient@example.test"},
	}
	var smtpErr *smtpclient.SMTPError
	if err := session.Data(strings.NewReader("From: Impostor <impostor@example.test>\r\nSubject: test\r\n\r\nbody\r\n")); !errors.As(err, &smtpErr) || smtpErr.Code != 553 {
		t.Fatalf("expected SMTP 553 for an unapproved RFC 5322 From author, got %v", err)
	}
}
