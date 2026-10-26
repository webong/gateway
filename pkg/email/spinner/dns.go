package spinner

import (
	"context"
	"errors"
	"net"
	"strings"

	mdns "github.com/miekg/dns"
	"github.com/webong/gateway/arranger/integration"
)

// DNSResolver adapts the language-neutral Gateway DNS capability to the DNS
// interfaces used by Email. Email never imports the DNS Extension package.
type DNSResolver struct {
	resolver integration.DNSResolver
}

func NewDNSResolver(resolver integration.DNSResolver) (*DNSResolver, error) {
	if resolver == nil {
		return nil, errors.New("Email requires the Gateway DNS capability")
	}
	return &DNSResolver{resolver: resolver}, nil
}

func (r *DNSResolver) Resolve(ctx context.Context, query *mdns.Msg) (*mdns.Msg, error) {
	if r == nil || r.resolver == nil || query == nil {
		return nil, errors.New("Email DNS resolver is unavailable")
	}
	wire, err := query.Pack()
	if err != nil {
		return nil, err
	}
	responseWire, err := r.resolver.Exchange(ctx, wire)
	if err != nil {
		return nil, err
	}
	response := new(mdns.Msg)
	if err := response.Unpack(responseWire); err != nil {
		return nil, err
	}
	if !response.Response || response.Id != query.Id || len(response.Question) != len(query.Question) {
		return nil, errors.New("Gateway DNS returned an unrelated response")
	}
	return response, nil
}

func (r *DNSResolver) LookupTXT(ctx context.Context, name string) ([]string, error) {
	response, err := r.lookup(ctx, name, mdns.TypeTXT)
	if err != nil {
		return nil, err
	}
	values := make([]string, 0, len(response.Answer))
	for _, answer := range response.Answer {
		if record, ok := answer.(*mdns.TXT); ok {
			values = append(values, strings.Join(record.Txt, ""))
		}
	}
	return values, nil
}

func (r *DNSResolver) LookupIPAddr(ctx context.Context, name string) ([]net.IPAddr, error) {
	var result []net.IPAddr
	for _, kind := range []uint16{mdns.TypeA, mdns.TypeAAAA} {
		response, err := r.lookup(ctx, name, kind)
		if err != nil {
			return nil, err
		}
		for _, answer := range response.Answer {
			switch record := answer.(type) {
			case *mdns.A:
				result = append(result, net.IPAddr{IP: append(net.IP(nil), record.A...)})
			case *mdns.AAAA:
				result = append(result, net.IPAddr{IP: append(net.IP(nil), record.AAAA...)})
			}
		}
	}
	if len(result) == 0 {
		return nil, &net.DNSError{Err: "no address records", Name: name, IsNotFound: true}
	}
	return result, nil
}

func (r *DNSResolver) LookupDomain(ctx context.Context, name string) (string, error) {
	response, err := r.lookup(ctx, name, mdns.TypeA)
	if err != nil {
		return "", err
	}
	switch response.Rcode {
	case mdns.RcodeSuccess:
		return "exists", nil
	case mdns.RcodeNameError:
		return "nxdomain", nil
	default:
		return "", errors.New("Gateway DNS returned an unsuccessful domain response")
	}
}

func (r *DNSResolver) Lookup(ctx context.Context, name string) (string, error) {
	return r.LookupDomain(ctx, name)
}

func (r *DNSResolver) lookup(ctx context.Context, name string, kind uint16) (*mdns.Msg, error) {
	name = mdns.Fqdn(strings.TrimSpace(name))
	if _, ok := mdns.IsDomainName(name); !ok {
		return nil, errors.New("invalid DNS name")
	}
	query := new(mdns.Msg)
	query.SetQuestion(name, kind)
	query.RecursionDesired = true
	return r.Resolve(ctx, query)
}
