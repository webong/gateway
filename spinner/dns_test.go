package spinner

import (
	"context"
	"testing"

	mdns "github.com/miekg/dns"
)

type dnsExchangeFunc func(context.Context, []byte) ([]byte, error)

func (f dnsExchangeFunc) Exchange(ctx context.Context, wire []byte) ([]byte, error) {
	return f(ctx, wire)
}

func TestDNSResolverAdaptsGatewayDNSForEmail(t *testing.T) {
	resolver, err := NewDNSResolver(dnsExchangeFunc(func(_ context.Context, wire []byte) ([]byte, error) {
		query := new(mdns.Msg)
		if err := query.Unpack(wire); err != nil {
			return nil, err
		}
		response := new(mdns.Msg)
		response.SetReply(query)
		switch query.Question[0].Qtype {
		case mdns.TypeTXT:
			response.Answer = []mdns.RR{&mdns.TXT{Hdr: mdns.RR_Header{Name: query.Question[0].Name, Rrtype: mdns.TypeTXT, Class: mdns.ClassINET}, Txt: []string{"v=spf1 ", "-all"}}}
		case mdns.TypeA:
			response.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: query.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET}, A: []byte{192, 0, 2, 1}}}
		}
		return response.Pack()
	}))
	if err != nil {
		t.Fatal(err)
	}
	texts, err := resolver.LookupTXT(t.Context(), "example.test")
	if err != nil || len(texts) != 1 || texts[0] != "v=spf1 -all" {
		t.Fatalf("unexpected TXT result: %v err=%v", texts, err)
	}
	addresses, err := resolver.LookupIPAddr(t.Context(), "example.test")
	if err != nil || len(addresses) != 1 || addresses[0].IP.String() != "192.0.2.1" {
		t.Fatalf("unexpected address result: %v err=%v", addresses, err)
	}
}
