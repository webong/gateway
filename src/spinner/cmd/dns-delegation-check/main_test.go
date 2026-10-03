package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestVerify(t *testing.T) {
	config := checkConfig{
		zone: "hooks.example.com", parent: "127.0.0.1:5300", parentZone: "example.com", resolver: "127.0.0.1:5303",
		nodes:       []string{"127.0.0.1:5301", "127.0.0.1:5302"},
		nameservers: []string{"ns1.example.net", "ns2.example.net"},
		probe:       "hello.hooks.example.com", negativeProbe: "hello.unknown.hooks.example.com", probeType: dns.TypeTXT, allowPrivate: true,
	}
	base := func(_ context.Context, server, network, name string, recordType uint16) (*dns.Msg, error) {
		message := &dns.Msg{MsgHdr: dns.MsgHdr{Response: true, Rcode: dns.RcodeSuccess}}
		if server != "127.0.0.1:5300" || name == "example.com." {
			message.Authoritative = true
		}
		switch {
		case recordType == dns.TypeSOA && name == "example.com.":
			message.Answer = []dns.RR{mustRR(t, "example.com. 300 IN SOA ns1.example.net. hostmaster.example.net. 1 3600 600 86400 300")}
		case recordType == dns.TypeNS:
			message.Answer = []dns.RR{
				mustRR(t, "hooks.example.com. 300 IN NS ns1.example.net."),
				mustRR(t, "hooks.example.com. 300 IN NS ns2.example.net."),
			}
		case recordType == dns.TypeSOA:
			message.Answer = []dns.RR{mustRR(t, "hooks.example.com. 300 IN SOA ns1.example.net. hostmaster.example.net. 1 3600 600 86400 300")}
		case recordType == dns.TypeTXT:
			if name == "hello.unknown.hooks.example.com." {
				message.Rcode = dns.RcodeNameError
				message.Ns = []dns.RR{mustRR(t, "hooks.example.com. 300 IN SOA ns1.example.net. hostmaster.example.net. 1 3600 600 86400 300")}
			} else {
				message.Answer = []dns.RR{mustRR(t, "hello.hooks.example.com. 300 IN TXT \"ok\"")}
			}
		}
		return message, nil
	}
	lookup := func(_ context.Context, server, name string) ([]net.IP, error) {
		if server != "127.0.0.1:5303" {
			return nil, errors.New("wrong resolver")
		}
		switch name {
		case "ns1.example.net.":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		case "ns2.example.net.":
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		default:
			return nil, errors.New("unknown nameserver")
		}
	}
	if err := verify(context.Background(), config, base, lookup); err != nil {
		t.Fatalf("valid delegation: %v", err)
	}
	nodata := func(ctx context.Context, server, network, name string, typ uint16) (*dns.Msg, error) {
		message, err := base(ctx, server, network, name, typ)
		if name == "hello.hooks.example.com." && typ == dns.TypeTXT {
			message.Answer = nil
			message.Ns = []dns.RR{mustRR(t, "hooks.example.com. 300 IN SOA ns1.example.net. hostmaster.example.net. 1 3600 600 86400 300")}
		}
		return message, err
	}
	if err := verify(context.Background(), config, nodata, lookup); err != nil {
		t.Fatalf("registered hook NODATA: %v", err)
	}
	parentTCPRetry := false
	truncatedParent := func(ctx context.Context, server, network, name string, typ uint16) (*dns.Msg, error) {
		message, err := base(ctx, server, network, name, typ)
		if server == "127.0.0.1:5300" {
			if network == "udp" {
				message.Truncated = true
				message.Answer = nil
			} else {
				parentTCPRetry = true
			}
		}
		return message, err
	}
	if err := verify(context.Background(), config, truncatedParent, lookup); err != nil || !parentTCPRetry {
		t.Fatalf("truncated parent delegation did not retry TCP: error=%v retried=%v", err, parentTCPRetry)
	}
	cases := []struct {
		name   string
		change func(*dns.Msg, string, string, string, uint16)
		want   string
	}{
		{"wrong parent zone", func(m *dns.Msg, server, _, name string, typ uint16) {
			if server == "127.0.0.1:5300" && name == "example.com." && typ == dns.TypeSOA {
				m.Authoritative = false
			}
		}, "not authoritative"},
		{"child answer instead of parent referral", func(m *dns.Msg, server, _, name string, typ uint16) {
			if server == "127.0.0.1:5300" && name == "hooks.example.com." && typ == dns.TypeNS {
				m.Authoritative = true
			}
		}, "does not delegate"},
		{"wrong parent delegation", func(m *dns.Msg, server, _, _ string, typ uint16) {
			if server == "127.0.0.1:5300" && typ == dns.TypeNS {
				m.Answer = m.Answer[:1]
			}
		}, "parent"},
		{"non-authoritative node", func(m *dns.Msg, server, _, _ string, typ uint16) {
			if server == "127.0.0.1:5302" && typ == dns.TypeNS {
				m.Authoritative = false
			}
		}, "authoritative"},
		{"inconsistent SOA", func(m *dns.Msg, server, _, _ string, typ uint16) {
			if server == "127.0.0.1:5302" && typ == dns.TypeSOA {
				m.Answer = []dns.RR{mustRR(t, "hooks.example.com. 300 IN SOA ns1.example.net. hostmaster.example.net. 2 3600 600 86400 300")}
			}
		}, "SOA differs"},
		{"missing hook", func(m *dns.Msg, server, network, _ string, typ uint16) {
			if server == "127.0.0.1:5302" && network == "tcp" && typ == dns.TypeTXT {
				m.Answer = nil
			}
		}, "registered-hook"},
		{"unregistered hook accepted", func(m *dns.Msg, server, network, name string, typ uint16) {
			if server == "127.0.0.1:5302" && network == "tcp" && name == "hello.unknown.hooks.example.com." && typ == dns.TypeTXT {
				m.Rcode = dns.RcodeSuccess
			}
		}, "unregistered hook"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			exchange := func(ctx context.Context, server, network, name string, typ uint16) (*dns.Msg, error) {
				message, err := base(ctx, server, network, name, typ)
				test.change(message, server, network, name, typ)
				return message, err
			}
			if err := verify(context.Background(), config, exchange, lookup); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verify error = %v, want %q", err, test.want)
			}
		})
	}
	wrongAddress := func(ctx context.Context, server, name string) ([]net.IP, error) {
		if name == "ns2.example.net." {
			return []net.IP{net.ParseIP("127.0.0.2")}, nil
		}
		return lookup(ctx, server, name)
	}
	if err := verify(context.Background(), config, base, wrongAddress); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("wrong nameserver address was accepted: %v", err)
	}
	config.nameservers = config.nameservers[:1]
	if err := verify(context.Background(), config, base, lookup); err == nil || !strings.Contains(err.Error(), "same number") {
		t.Fatalf("mismatched nameserver count was accepted: %v", err)
	}
}

func TestServerAddressRejectsPrivateByDefault(t *testing.T) {
	if _, err := serverAddress("127.0.0.1", false); err == nil {
		t.Fatal("loopback address accepted for public verification")
	}
	if _, err := serverAddress("dns.example.com", false); err == nil {
		t.Fatal("hostname accepted instead of numeric address")
	}
	if address, err := serverAddress("[2001:4860:4860::8888]:53", false); err != nil || address != "[2001:4860:4860::8888]:53" {
		t.Fatalf("public IPv6 address = %q, %v", address, err)
	}
}

func TestLookupNameserverIPsOverDNS(t *testing.T) {
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	aRecord := mustRR(t, "ns1.example.net. 300 IN A 192.0.2.10")
	aaaaRecord := mustRR(t, "ns1.example.net. 300 IN AAAA 2001:db8::10")
	server := &dns.Server{PacketConn: packet, Handler: dns.HandlerFunc(func(writer dns.ResponseWriter, query *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(query)
		response.RecursionAvailable = true
		if !query.RecursionDesired || len(query.Question) != 1 {
			response.Rcode = dns.RcodeFormatError
		} else {
			switch query.Question[0].Qtype {
			case dns.TypeA:
				response.Answer = []dns.RR{aRecord}
			case dns.TypeAAAA:
				response.Answer = []dns.RR{aaaaRecord}
			}
		}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	addresses, err := lookupNameserverIPs(context.Background(), packet.LocalAddr().String(), "ns1.example.net.")
	if err != nil || len(addresses) != 2 || !addresses[0].Equal(net.ParseIP("192.0.2.10")) || !addresses[1].Equal(net.ParseIP("2001:db8::10")) {
		t.Fatalf("recursive nameserver addresses = %v, %v", addresses, err)
	}
}

func mustRR(t *testing.T, value string) dns.RR {
	t.Helper()
	record, err := dns.NewRR(value)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
