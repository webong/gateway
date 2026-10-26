package spinner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	mdns "github.com/miekg/dns"
)

const defaultResolverTimeout = 3 * time.Second

// RecursiveResolver provides bounded recursive DNS exchange for other Gateway
// Extensions. It is separate from the authoritative listener: depending on
// DNS resolution does not force a public port-53 listener to be enabled.
type RecursiveResolver struct {
	servers []string
	timeout time.Duration
}

func NewRecursiveResolver(address string, timeout time.Duration) (*RecursiveResolver, error) {
	servers := []string(nil)
	if address != "" {
		if err := validResolverAddress(address); err != nil {
			return nil, err
		}
		servers = []string{address}
	} else {
		config, err := mdns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil || len(config.Servers) == 0 {
			return nil, errors.New("DNS resolver requires an explicit IP endpoint or a system nameserver")
		}
		port := config.Port
		if port == "" {
			port = "53"
		}
		for _, server := range config.Servers {
			if net.ParseIP(server) == nil {
				return nil, errors.New("system DNS resolver contains a non-IP nameserver")
			}
			servers = append(servers, net.JoinHostPort(server, port))
		}
	}
	if timeout <= 0 {
		timeout = defaultResolverTimeout
	}
	if timeout < 10*time.Millisecond || timeout > 30*time.Second {
		return nil, errors.New("DNS resolver timeout must be between 10ms and 30s")
	}
	return &RecursiveResolver{servers: servers, timeout: timeout}, nil
}

func validResolverAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil {
		return errors.New("DNS resolver address must be an IP address with a port")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("DNS resolver port is invalid")
	}
	return nil
}

func (r *RecursiveResolver) Exchange(ctx context.Context, wire []byte) ([]byte, error) {
	if r == nil || len(r.servers) == 0 || len(wire) == 0 || len(wire) > 65535 {
		return nil, errors.New("DNS resolver is unavailable or the query is invalid")
	}
	query := new(mdns.Msg)
	if err := query.Unpack(wire); err != nil || query.Response || query.Opcode != mdns.OpcodeQuery || len(query.Question) != 1 {
		return nil, errors.New("DNS resolver requires one standard query")
	}
	question := query.Question[0]
	if question.Qclass != mdns.ClassINET || question.Qtype == mdns.TypeANY || question.Qtype == mdns.TypeAXFR || question.Qtype == mdns.TypeIXFR {
		return nil, errors.New("DNS resolver query type is not allowed")
	}
	if _, ok := mdns.IsDomainName(question.Name); !ok {
		return nil, errors.New("DNS resolver query name is invalid")
	}
	query.RecursionDesired = true
	var lastErr error
	for _, server := range r.servers {
		response, _, err := (&mdns.Client{Net: "udp", Timeout: r.timeout}).ExchangeContext(ctx, query, server)
		if err != nil {
			lastErr = err
			continue
		}
		if response != nil && response.Truncated {
			response, _, err = (&mdns.Client{Net: "tcp", Timeout: r.timeout}).ExchangeContext(ctx, query, server)
			if err != nil {
				lastErr = err
				continue
			}
		}
		if !matchingRecursiveResponse(query, response) {
			lastErr = errors.New("DNS upstream returned an unrelated response")
			continue
		}
		packed, err := response.Pack()
		if err != nil || len(packed) > 65535 {
			return nil, errors.New("DNS upstream returned an invalid response")
		}
		return packed, nil
	}
	if lastErr == nil {
		lastErr = errors.New("DNS resolver has no available upstream")
	}
	return nil, fmt.Errorf("DNS resolution failed: %w", lastErr)
}

func matchingRecursiveResponse(query, response *mdns.Msg) bool {
	if query == nil || response == nil || !response.Response || response.Truncated || response.Id != query.Id || len(response.Question) != 1 {
		return false
	}
	return response.Question[0] == query.Question[0]
}
