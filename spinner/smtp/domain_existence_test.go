package smtp

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	bridge "github.com/webong/gateway/arranger/protocol"
)

type domainExistenceFunc func(context.Context, string) (string, error)

func (lookup domainExistenceFunc) Lookup(ctx context.Context, domain string) (string, error) {
	return lookup(ctx, domain)
}

func TestDomainExistenceUsesDNSResponseCodeNotAnswerCount(t *testing.T) {
	listener, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &dns.Server{PacketConn: listener, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(query)
		switch query.Question[0].Name {
		case "missing.example.test.":
			response.Rcode = dns.RcodeNameError
		case "broken.example.test.":
			response.Rcode = dns.RcodeServerFailure
		}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	resolver, err := NewDomainExistenceResolver(listener.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		domain  string
		want    string
		wantErr bool
	}{
		{domain: "existing.example.test", want: "exists"}, // NOERROR with no A records is NODATA.
		{domain: "missing.example.test", want: "nxdomain"},
		{domain: "broken.example.test", wantErr: true},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		got, err := resolver.Lookup(ctx, test.domain)
		cancel()
		if (err != nil) != test.wantErr || got != test.want {
			t.Fatalf("Lookup(%q) = %q, %v; want %q, error=%v", test.domain, got, err, test.want, test.wantErr)
		}
	}
}

func TestDomainExistenceRejectsUntrustedResolverOrInvalidDomain(t *testing.T) {
	for _, address := range []string{"resolver.example.test:53", "127.0.0.1:0", "not-an-address"} {
		if _, err := NewDomainExistenceResolver(address); err == nil {
			t.Fatalf("accepted resolver address %q", address)
		}
	}
	resolver, err := NewDomainExistenceResolver("127.0.0.1:53")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Lookup(context.Background(), "bad..example.test"); err == nil {
		t.Fatal("accepted malformed author domain")
	}
}

func TestSMTPTransactionCarriesTrustedAuthorExistenceEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		lookup   domainExistenceFunc
		expected string
	}{
		{name: "nxdomain", expected: "nxdomain", lookup: func(_ context.Context, domain string) (string, error) {
			if domain != "missing.example.test" {
				return "", errors.New("wrong author domain")
			}
			return "nxdomain", nil
		}},
		{name: "lookup error", expected: "temperror", lookup: func(context.Context, string) (string, error) {
			return "", errors.New("DNS unavailable")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var planned bridge.GatewayEvent
			s := &session{
				planner: plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
					planned = event
					return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept}, nil
				}),
				executor: &recordingExecutor{},
				config:   Config{MaxMessageSize: 1024, PlannerTimeout: time.Second, DomainExistenceResolver: test.lookup, DomainExistenceTimeout: time.Second},
				from:     "sender@example.test", recipients: []string{"inbox@example.test"},
			}
			if err := s.Data(strings.NewReader("From: Sender <sender@missing.example.test>\r\n\r\nbody\r\n")); err != nil {
				t.Fatal(err)
			}
			if got := planned.Attributes["rfc5322_from_existence"]; got != test.expected {
				t.Fatalf("unexpected author-domain evidence %q, want %q", got, test.expected)
			}
		})
	}
}
