package smtp

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

type spfResolverFunc func(context.Context, *dns.Msg) (*dns.Msg, error)

func (f spfResolverFunc) Resolve(ctx context.Context, message *dns.Msg) (*dns.Msg, error) {
	return f(ctx, message)
}

func TestSPFUsesEnvelopeDomainAndPeerIP(t *testing.T) {
	queries := 0
	evaluator := dnsSPFEvaluator{resolver: spfResolverFunc(func(_ context.Context, query *dns.Msg) (*dns.Msg, error) {
		queries++
		if len(query.Question) != 1 || query.Question[0].Name != "sender.example." {
			t.Fatalf("unexpected SPF query: %+v", query.Question)
		}
		answer := new(dns.Msg)
		answer.SetReply(query)
		answer.Answer = []dns.RR{&dns.TXT{Hdr: dns.RR_Header{Name: "sender.example.", Rrtype: dns.TypeTXT, Class: dns.ClassINET}, Txt: []string{"v=spf1 ip4:192.0.2.0/24 -all"}}}
		return answer, nil
	})}
	for _, tc := range []struct {
		ip   string
		want string
	}{
		{"192.0.2.5", "pass"},
		{"198.51.100.5", "fail"},
	} {
		evidence := evaluator.Evaluate(context.Background(), net.ParseIP(tc.ip), "alice@sender.example", "different.example")
		if evidence.Result != tc.want || evidence.Identity != "mailfrom" || evidence.Domain != "sender.example" {
			t.Fatalf("SPF(%s) = %+v, want %s", tc.ip, evidence, tc.want)
		}
	}
	if queries != 2 {
		t.Fatalf("expected two DNS queries, got %d", queries)
	}
}

func TestSPFNullSenderUsesHeloAndNoIdentitySkipsDNS(t *testing.T) {
	queries := 0
	evaluator := dnsSPFEvaluator{resolver: spfResolverFunc(func(_ context.Context, query *dns.Msg) (*dns.Msg, error) {
		queries++
		answer := new(dns.Msg)
		answer.SetReply(query)
		return answer, nil
	})}
	evidence := evaluator.Evaluate(context.Background(), net.ParseIP("192.0.2.5"), "", "helo.example")
	if evidence.Result != "none" || evidence.Identity != "helo" || evidence.Domain != "helo.example" {
		t.Fatalf("unexpected HELO evidence: %+v", evidence)
	}
	evidence = evaluator.Evaluate(context.Background(), net.ParseIP("192.0.2.5"), "", "")
	if evidence.Result != "none" || evidence.Identity != "" || queries != 1 {
		t.Fatalf("expected no second DNS query: evidence=%+v queries=%d", evidence, queries)
	}
}

func TestSPFIPv6Policy(t *testing.T) {
	evaluator := dnsSPFEvaluator{resolver: spfResolverFunc(func(_ context.Context, query *dns.Msg) (*dns.Msg, error) {
		answer := new(dns.Msg)
		answer.SetReply(query)
		answer.Answer = []dns.RR{&dns.TXT{Hdr: dns.RR_Header{Name: "sender.example.", Rrtype: dns.TypeTXT, Class: dns.ClassINET}, Txt: []string{"v=spf1 ip6:2001:db8::/32 -all"}}}
		return answer, nil
	})}
	evidence := evaluator.Evaluate(context.Background(), net.ParseIP("2001:db8::1"), "alice@sender.example", "")
	if evidence.Result != "pass" {
		t.Fatalf("unexpected IPv6 SPF evidence: %+v", evidence)
	}
}

func TestSPFDNSErrorAndDeadlineAreTemporaryEvidence(t *testing.T) {
	evaluator := dnsSPFEvaluator{resolver: spfResolverFunc(func(context.Context, *dns.Msg) (*dns.Msg, error) {
		return nil, errors.New("DNS unavailable")
	})}
	evidence := evaluator.Evaluate(context.Background(), net.ParseIP("192.0.2.5"), "alice@sender.example", "")
	if evidence.Result != "temperror" {
		t.Fatalf("unexpected DNS failure evidence: %+v", evidence)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	evidence = evaluator.Evaluate(ctx, net.ParseIP("192.0.2.5"), "alice@sender.example", "")
	if evidence.Result != "temperror" {
		t.Fatalf("unexpected deadline evidence: %+v", evidence)
	}
}

func TestSPFUntrustedOrMissingPeerIsUnavailable(t *testing.T) {
	called := false
	s := &session{from: "alice@sender.example", remoteAddr: "not-an-ip:25", config: Config{SPFEvaluator: spfEvaluatorFunc(func(context.Context, net.IP, string, string) SPFEvidence {
		called = true
		return SPFEvidence{Result: "pass"}
	})}}
	attributes := s.spfAttributes(context.Background())
	if attributes["spf_result"] != "unavailable" || called {
		t.Fatalf("untrusted source must not produce SPF pass: %+v", attributes)
	}
}
