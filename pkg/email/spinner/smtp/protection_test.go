package smtp

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/arranger/integration"
)

type scannerFunc func(context.Context, []byte) (ScanVerdict, error)

func (function scannerFunc) Scan(ctx context.Context, value []byte) (ScanVerdict, error) {
	return function(ctx, value)
}

func TestSMTPPlannerQuarantineStoresBeforeAcknowledgment(t *testing.T) {
	message := "From: Alice <alice@example.test>\r\n\r\nbody\r\n"
	for _, test := range []struct {
		name     string
		storeErr error
		missing  bool
		wantCode int
	}{
		{name: "persisted"},
		{name: "storage unavailable", missing: true, wantCode: 451},
		{name: "storage failed", storeErr: errors.New("disk full"), wantCode: 451},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored := false
			configuration := Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, ScanTimeout: time.Second}
			if !test.missing {
				configuration.Quarantine = quarantineFunc(func(_ context.Context, value QuarantinedMessage) (string, error) {
					stored = true
					if value.EnvelopeFrom != "sender@example.test" || len(value.Recipients) != 1 || value.Recipients[0] != "inbox@example.test" || value.Reason != "DMARC policy" || string(value.Payload) != message {
						t.Fatalf("quarantine lost the transaction: %+v", value)
					}
					return "opaque-id", test.storeErr
				})
			}
			executor := &recordingExecutor{}
			s := &session{planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayQuarantine}, nil
			}), executor: executor, config: configuration, from: "sender@example.test", recipients: []string{"inbox@example.test"}}
			err := s.Data(strings.NewReader(message))
			if test.wantCode == 0 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var smtpError *smtpclient.SMTPError
				if !errors.As(err, &smtpError) || smtpError.Code != test.wantCode {
					t.Fatalf("expected SMTP %d, got %v", test.wantCode, err)
				}
			}
			if stored == test.missing || len(executor.snapshot()) != 0 {
				t.Fatalf("unexpected quarantine/relay state: stored=%v relays=%d", stored, len(executor.snapshot()))
			}
		})
	}
}

func TestSMTPClamdQuarantineOverWire(t *testing.T) {
	clamd, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clamdResult := make(chan error, 1)
	go func() {
		connection, err := clamd.Accept()
		if err != nil {
			clamdResult <- err
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		command, err := reader.ReadString(0)
		if err != nil || command != "zINSTREAM\x00" {
			clamdResult <- errors.New("incorrect clamd command")
			return
		}
		var message []byte
		for {
			var size uint32
			if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
				clamdResult <- err
				return
			}
			if size == 0 {
				break
			}
			chunk := make([]byte, size)
			if _, err := io.ReadFull(reader, chunk); err != nil {
				clamdResult <- err
				return
			}
			message = append(message, chunk...)
		}
		if string(message) != "Subject: infected\r\n\r\nbody\r\n" {
			clamdResult <- errors.New("clamd received wrong message")
			return
		}
		_, err = io.WriteString(connection, "stream: Eicar-Test-Signature FOUND\x00")
		clamdResult <- err
	}()

	scanner, err := NewClamdScanner("tcp", clamd.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	quarantine, err := NewFileQuarantine(filepath.Join(t.TempDir(), "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	server, err := NewServer(Config{
		Address: "127.0.0.1:0", Hostname: "smtp.example.test", PlannerTimeout: time.Second,
		Scanner: scanner, ScanTimeout: time.Second, Quarantine: quarantine,
		SPFEvaluator: spfEvaluatorFunc(func(context.Context, net.IP, string, string) SPFEvidence {
			return SPFEvidence{Result: "none"}
		}),
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
			Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: "https://example.test/mail"}},
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
		_ = clamd.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ErrServerClosed) {
			t.Errorf("shutdown SMTP: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve SMTP: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP did not stop")
		}
	})
	send := func(subject string) error {
		client, err := smtpclient.Dial(listener.Addr().String())
		if err != nil {
			return err
		}
		defer client.Close()
		if err := client.Mail("sender@example.test", nil); err != nil {
			return err
		}
		if err := client.Rcpt("recipient@example.test", nil); err != nil {
			return err
		}
		data, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := io.WriteString(data, "Subject: "+subject+"\r\n\r\nbody\r\n"); err != nil {
			return err
		}
		return data.Close()
	}
	if err := send("infected"); err != nil {
		t.Fatalf("infected message was not quarantined: %v", err)
	}
	select {
	case err := <-clamdResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clamd fixture did not receive the SMTP message")
	}
	if err := clamd.Close(); err != nil {
		t.Fatal(err)
	}
	var smtpError *smtpclient.SMTPError
	if err := send("scanner-offline"); !errors.As(err, &smtpError) || smtpError.Code != 451 {
		t.Fatalf("scanner outage should defer DATA with 451: %v", err)
	}
	records, err := quarantine.List()
	if err != nil || len(records) != 1 || records[0].EnvelopeFrom != "sender@example.test" || records[0].Reason != "Eicar-Test-Signature" {
		t.Fatalf("unexpected quarantine records: %+v, %v", records, err)
	}
	_, payload, err := quarantine.Read(records[0].ID)
	if err != nil || string(payload) != "Subject: infected\r\n\r\nbody\r\n" {
		t.Fatalf("quarantined message differs from SMTP DATA: %q, %v", payload, err)
	}
	if len(executor.snapshot()) != 0 {
		t.Fatal("blocked messages reached delivery")
	}
}

func TestSMTPRealClamdQuarantine(t *testing.T) {
	address := os.Getenv("GATEWAY_TEST_CLAMD_ADDR")
	if address == "" {
		t.Skip("set GATEWAY_TEST_CLAMD_ADDR to run against a real clamd service")
	}
	scanner, err := NewClamdScanner("tcp", address, 20*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	quarantine, err := NewFileQuarantine(filepath.Join(t.TempDir(), "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{}
	server, err := NewServer(Config{
		Address: "127.0.0.1:0", Hostname: "smtp.example.test", PlannerTimeout: time.Second,
		Scanner: scanner, ScanTimeout: 20 * time.Second, Quarantine: quarantine,
		SPFEvaluator: spfEvaluatorFunc(func(context.Context, net.IP, string, string) SPFEvidence {
			return SPFEvidence{Result: "none"}
		}),
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
			Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: "https://example.test/mail"}},
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
			t.Errorf("shutdown SMTP: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve SMTP: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP did not stop")
		}
	})
	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
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
	const eicar = "X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*"
	if _, err := io.WriteString(data, eicar); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatalf("infected SMTP DATA was not acknowledged after quarantine: %v", err)
	}
	records, err := quarantine.List()
	if err != nil || len(records) != 1 || records[0].Reason == "" {
		t.Fatalf("real clamd did not quarantine infected SMTP message: %+v, %v", records, err)
	}
	if len(executor.snapshot()) != 0 {
		t.Fatal("infected SMTP message reached delivery")
	}
}

type quarantineFunc func(context.Context, QuarantinedMessage) (string, error)

func (function quarantineFunc) Store(ctx context.Context, value QuarantinedMessage) (string, error) {
	return function(ctx, value)
}

type pruningQuarantine struct {
	called chan time.Time
	err    error
}

func (quarantine *pruningQuarantine) Store(context.Context, QuarantinedMessage) (string, error) {
	return "test-id", nil
}

func (quarantine *pruningQuarantine) PruneBefore(cutoff time.Time) (int, error) {
	quarantine.called <- cutoff
	return 0, quarantine.err
}

func TestSMTPQuarantineRetentionPrunesBeforeServing(t *testing.T) {
	for _, test := range []struct {
		name      string
		pruneErr  error
		wantError bool
	}{
		{name: "healthy"},
		{name: "prune failure", pruneErr: errors.New("unreadable quarantine"), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			quarantine := &pruningQuarantine{called: make(chan time.Time, 1), err: test.pruneErr}
			before := time.Now().UTC().Add(-time.Hour)
			server, err := NewServer(Config{
				Address: "127.0.0.1:0", Scanner: scannerFunc(func(context.Context, []byte) (ScanVerdict, error) {
					return ScanVerdict{}, nil
				}),
				Quarantine: quarantine, QuarantineRetention: time.Hour,
			}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept}, nil
			}), &recordingExecutor{})
			select {
			case cutoff := <-quarantine.called:
				if cutoff.Before(before) || cutoff.After(time.Now().UTC().Add(-time.Hour)) {
					t.Fatalf("unexpected retention cutoff: %v", cutoff)
				}
			case <-time.After(time.Second):
				t.Fatal("quarantine was not pruned during SMTP construction")
			}
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "prune SMTP quarantine") {
					t.Fatalf("SMTP initialized despite unsafe retention state: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			served := make(chan error, 1)
			go func() { served <- server.Serve(listener) }()
			_ = listener.Close()
			select {
			case err := <-served:
				if err != nil && !errors.Is(err, ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("SMTP listener did not stop")
			}
		})
	}
}

func TestSMTPQuarantineRetentionRequiresPrunableStore(t *testing.T) {
	_, err := NewServer(Config{
		Address: "127.0.0.1:0", Scanner: scannerFunc(func(context.Context, []byte) (ScanVerdict, error) {
			return ScanVerdict{}, nil
		}),
		Quarantine:          quarantineFunc(func(context.Context, QuarantinedMessage) (string, error) { return "id", nil }),
		QuarantineRetention: time.Hour,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept}, nil
	}), &recordingExecutor{})
	if err == nil || !strings.Contains(err.Error(), "prunable quarantine store") {
		t.Fatalf("retention accepted a store without pruning: %v", err)
	}
}

func TestSMTPScansResolvedDeliveryBeforeHandoff(t *testing.T) {
	message := "Subject: scan\r\n\r\nbody\r\n"
	for _, test := range []struct {
		name            string
		verdict         ScanVerdict
		scanError       error
		quarantine      bool
		quarantineError error
		wantCode        int
		wantDeliveries  int
		wantStored      bool
	}{
		{name: "clean", wantDeliveries: 1},
		{name: "infected reject", verdict: ScanVerdict{Infected: true, Signature: "Eicar-Test-Signature"}, wantCode: 554},
		{name: "infected quarantine", verdict: ScanVerdict{Infected: true, Signature: "Eicar-Test-Signature"}, quarantine: true, wantStored: true},
		{name: "scanner outage", scanError: errors.New("offline"), wantCode: 451},
		{name: "quarantine failure", verdict: ScanVerdict{Infected: true}, quarantine: true, quarantineError: errors.New("disk full"), wantCode: 451},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{}
			stored := false
			configuration := Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, ScanTimeout: time.Second,
				Scanner: scannerFunc(func(_ context.Context, value []byte) (ScanVerdict, error) {
					if string(value) != message {
						t.Fatalf("scanner did not receive original bytes: %q", value)
					}
					return test.verdict, test.scanError
				}),
			}
			if test.quarantine {
				configuration.Quarantine = quarantineFunc(func(_ context.Context, value QuarantinedMessage) (string, error) {
					stored = true
					if value.EnvelopeFrom != "sender@example.test" || len(value.Recipients) != 1 || string(value.Payload) != message {
						t.Fatalf("quarantine lost original transaction: %+v", value)
					}
					return "opaque-id", test.quarantineError
				})
			}
			s := &session{
				planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
					return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP,
						Action: bridge.GatewayDeliver, Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: "https://example.test/mail"}}}, nil
				}),
				executor: executor, config: configuration,
				from: "sender@example.test", recipients: []string{"inbox@example.test"},
			}
			err := s.Data(strings.NewReader(message))
			if test.wantCode == 0 {
				if err != nil {
					t.Fatalf("unexpected SMTP error: %v", err)
				}
			} else {
				var smtpError *smtpclient.SMTPError
				if !errors.As(err, &smtpError) || smtpError.Code != test.wantCode {
					t.Fatalf("expected SMTP %d, got %v", test.wantCode, err)
				}
			}
			if got := len(executor.snapshot()); got != test.wantDeliveries {
				t.Fatalf("unexpected relays: %d", got)
			}
			if stored != test.wantStored && test.quarantineError == nil {
				t.Fatalf("unexpected quarantine status: %v", stored)
			}
		})
	}
}

func TestSMTPFailureNoticeRequiresAuthenticatedPlannerApproval(t *testing.T) {
	for _, test := range []struct {
		name          string
		authEnabled   bool
		authenticated bool
		metadata      map[string]string
		wantCode      int
		wantNotice    bool
	}{
		{name: "authenticated approved", authEnabled: true, authenticated: true, metadata: map[string]string{"failure_notice_to": "sender@example.test"}, wantNotice: true},
		{name: "no approval", authEnabled: true, authenticated: true},
		{name: "public SMTP cannot authorize", metadata: map[string]string{"failure_notice_to": "sender@example.test"}, wantCode: 451},
		{name: "wrong reverse path", authEnabled: true, authenticated: true, metadata: map[string]string{"failure_notice_to": "victim@example.test"}, wantCode: 451},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &recordingExecutor{}
			s := &session{
				config:        Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, AuthEnabled: test.authEnabled},
				authenticated: test.authenticated, from: "sender@example.test", recipients: []string{"inbox@example.test"},
				authorizedSenders: map[string]struct{}{"sender@example.test": {}},
				planner: plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
					return bridge.GatewayDecision{
						Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayDeliver,
						Metadata: test.metadata,
						Deliveries: []bridge.GatewayDelivery{{
							Protocol: bridge.ProtocolSMTP, Target: "inbox@example.test",
							Attributes: map[string]string{"mail_from": "sender@example.test", bridge.FailureNoticeToAttribute: "forged@example.test", bridge.DKIMSigningDomainAttribute: "forged.example"},
						}},
					}, nil
				}),
				executor: executor,
			}
			err := s.Data(strings.NewReader("From: sender@example.test\r\nSubject: test\r\n\r\nbody\r\n"))
			var smtpError *smtpclient.SMTPError
			if test.wantCode != 0 {
				if !errors.As(err, &smtpError) || smtpError.Code != test.wantCode || len(executor.snapshot()) != 0 {
					t.Fatalf("expected SMTP %d with no delivery, got %v and %+v", test.wantCode, err, executor.snapshot())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			deliveries := executor.snapshot()
			if len(deliveries) != 1 {
				t.Fatalf("expected one delivery, got %+v", deliveries)
			}
			want := ""
			if test.wantNotice {
				want = "sender@example.test"
			}
			if deliveries[0].Attributes[bridge.FailureNoticeToAttribute] != want {
				t.Fatalf("unsafe failure notice routing: %+v", deliveries[0].Attributes)
			}
			wantSigningDomain := ""
			if test.authEnabled {
				wantSigningDomain = "example.test"
			}
			if deliveries[0].Attributes[bridge.DKIMSigningDomainAttribute] != wantSigningDomain {
				t.Fatalf("unsafe DKIM signing domain: %+v", deliveries[0].Attributes)
			}
		})
	}
}
