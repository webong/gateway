package smtp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/miekg/dns"
)

// DomainExistenceResolver supplies an actual DNS response code for the RFC
// 5322 From domain. An empty answer with NOERROR means the name exists; only
// NXDOMAIN proves non-existence for DMARC's np policy.
type DomainExistenceResolver interface {
	Lookup(context.Context, string) (string, error)
}

type dnsDomainExistenceResolver struct{ address string }

// NewDomainExistenceResolver uses an operator-selected recursive DNS server.
// An explicit IP endpoint avoids resolving the resolver through itself.
func NewDomainExistenceResolver(address string) (DomainExistenceResolver, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return nil, errors.New("email domain resolver must be an IP address with a port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, errors.New("email domain resolver port is invalid")
	}
	return dnsDomainExistenceResolver{address: address}, nil
}

func (resolver dnsDomainExistenceResolver) Lookup(ctx context.Context, domain string) (string, error) {
	if normalizedMailDomain(domain) != domain {
		return "", errors.New("invalid email author domain")
	}
	name := dns.Fqdn(domain)
	if _, ok := dns.IsDomainName(name); !ok {
		return "", errors.New("invalid email author domain")
	}
	query := new(dns.Msg)
	query.SetQuestion(name, dns.TypeA)
	query.RecursionDesired = true
	response, _, err := (&dns.Client{Net: "udp"}).ExchangeContext(ctx, query, resolver.address)
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", errors.New("email domain resolver returned no DNS response")
	}
	if response.Truncated {
		response, _, err = (&dns.Client{Net: "tcp"}).ExchangeContext(ctx, query, resolver.address)
		if err != nil {
			return "", err
		}
		if response == nil {
			return "", errors.New("email domain resolver returned no DNS response")
		}
	}
	if !response.Response || response.Id != query.Id || response.Truncated || len(response.Question) != 1 || response.Question[0].Name != name || response.Question[0].Qtype != dns.TypeA || response.Question[0].Qclass != dns.ClassINET {
		return "", errors.New("email domain resolver returned an unrelated DNS response")
	}
	switch response.Rcode {
	case dns.RcodeSuccess:
		return "exists", nil
	case dns.RcodeNameError:
		return "nxdomain", nil
	default:
		return "", fmt.Errorf("email domain resolver returned DNS status %d", response.Rcode)
	}
}
