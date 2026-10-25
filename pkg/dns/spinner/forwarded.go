package spinner

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	mdns "github.com/miekg/dns"
)

const defaultForwardedTimeout = 2 * time.Second

var forwarderName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// Forwarder is one upstream DNS answer source. Domains and client
// networks form an explicit forwarding policy; no implicit catch-all is
// created when the list is empty.
type Forwarder struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Transport   string   `json:"transport"`
	Domains     []string `json:"domains"`
	ClientCIDRs []string `json:"client_cidrs"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
}

type forwarderRoute struct {
	name      string
	address   string
	transport string
	domain    string
	client    netip.Prefix
	timeout   time.Duration
}

func normalizeForwarders(forwarders []Forwarder) ([]forwarderRoute, error) {
	if len(forwarders) > 32 {
		return nil, fmt.Errorf("DNS forwarder count exceeds 32")
	}
	routes := make([]forwarderRoute, 0)
	seenNames := make(map[string]bool, len(forwarders))
	seenRoutes := make(map[string]bool)
	for _, forwarder := range forwarders {
		forwarder.Name = strings.ToLower(strings.TrimSpace(forwarder.Name))
		if !forwarderName.MatchString(forwarder.Name) || seenNames[forwarder.Name] {
			return nil, fmt.Errorf("invalid or duplicate DNS forwarder name %q", forwarder.Name)
		}
		seenNames[forwarder.Name] = true
		if err := validateForwardedAddress(forwarder.Address); err != nil {
			return nil, fmt.Errorf("DNS forwarder %s: %w", forwarder.Name, err)
		}
		forwarder.Transport = strings.ToLower(strings.TrimSpace(forwarder.Transport))
		if forwarder.Transport == "" {
			forwarder.Transport = "udp"
		}
		if forwarder.Transport != "udp" && forwarder.Transport != "tcp" {
			return nil, fmt.Errorf("DNS forwarder %s has unsupported transport %q", forwarder.Name, forwarder.Transport)
		}
		if len(forwarder.Domains) == 0 || len(forwarder.Domains) > 32 || len(forwarder.ClientCIDRs) == 0 || len(forwarder.ClientCIDRs) > 32 {
			return nil, fmt.Errorf("DNS forwarder %s requires 1 to 32 domains and client networks", forwarder.Name)
		}
		timeout := defaultForwardedTimeout
		if forwarder.TimeoutMS != 0 {
			if forwarder.TimeoutMS < 10 || forwarder.TimeoutMS > 5000 {
				return nil, fmt.Errorf("DNS forwarder %s timeout must be between 10 and 5000 milliseconds", forwarder.Name)
			}
			timeout = time.Duration(forwarder.TimeoutMS) * time.Millisecond
		}

		domains := make([]string, 0, len(forwarder.Domains))
		for _, value := range forwarder.Domains {
			domain := canonicalName(value)
			if domain != "." && !validAuthorityName(domain) {
				return nil, fmt.Errorf("DNS forwarder %s has invalid domain %q", forwarder.Name, value)
			}
			domains = append(domains, domain)
		}
		clients := make([]netip.Prefix, 0, len(forwarder.ClientCIDRs))
		for _, value := range forwarder.ClientCIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix != prefix.Masked() || prefix.Addr().IsUnspecified() {
				return nil, fmt.Errorf("DNS forwarder %s has invalid client network %q", forwarder.Name, value)
			}
			clients = append(clients, prefix)
		}
		for _, domain := range domains {
			for _, client := range clients {
				key := domain + "\n" + client.String()
				if seenRoutes[key] {
					return nil, fmt.Errorf("duplicate DNS forwarded route for %s and %s", domain, client)
				}
				seenRoutes[key] = true
				routes = append(routes, forwarderRoute{name: forwarder.Name, address: forwarder.Address,
					transport: forwarder.Transport, domain: domain, client: client, timeout: timeout})
			}
		}
	}
	sort.Slice(routes, func(left, right int) bool {
		if len(routes[left].domain) != len(routes[right].domain) {
			return len(routes[left].domain) > len(routes[right].domain)
		}
		if routes[left].client.Bits() != routes[right].client.Bits() {
			return routes[left].client.Bits() > routes[right].client.Bits()
		}
		return routes[left].name < routes[right].name
	})
	return routes, nil
}

func validateForwardedAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return fmt.Errorf("invalid address %q", address)
	}
	if parsed := net.ParseIP(host); parsed == nil && !validAuthorityName(canonicalName(host)) {
		return fmt.Errorf("invalid address %q", address)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("invalid address %q", address)
	}
	return nil
}

func (s *Server) forwarderFor(name string, remote net.Addr) *forwarderRoute {
	client, ok := forwardedClientAddress(remote)
	if !ok {
		return nil
	}
	for index := range s.forwarders {
		route := &s.forwarders[index]
		if route.client.Contains(client) && mdns.IsSubDomain(route.domain, name) {
			return route
		}
	}
	return nil
}

func forwardedClientAddress(address net.Addr) (netip.Addr, bool) {
	if address == nil {
		return netip.Addr{}, false
	}
	var raw string
	switch value := address.(type) {
	case *net.UDPAddr:
		raw = value.IP.String()
	case *net.TCPAddr:
		raw = value.IP.String()
	default:
		host, _, err := net.SplitHostPort(address.String())
		if err != nil {
			return netip.Addr{}, false
		}
		raw = host
	}
	parsed, err := netip.ParseAddr(raw)
	return parsed.Unmap(), err == nil
}

func (s *Server) forwardQuery(writer mdns.ResponseWriter, request *mdns.Msg, forwarder forwarderRoute, observed *dnsObservation) {
	ctx, cancel := context.WithTimeout(context.Background(), forwarder.timeout)
	defer cancel()
	response, err := exchangeForwarded(ctx, request, forwarder)
	if err != nil || response == nil {
		response = new(mdns.Msg)
		response.SetRcode(request, mdns.RcodeServerFailure)
		observed.failure = "forwarded_upstream"
	} else {
		response.Id = request.Id
	}
	s.writeObservedResponse(writer, request, response, observed)
}

func exchangeForwarded(ctx context.Context, request *mdns.Msg, forwarder forwarderRoute) (*mdns.Msg, error) {
	client := &mdns.Client{Net: forwarder.transport, Timeout: forwarder.timeout}
	response, _, err := client.ExchangeContext(ctx, request.Copy(), forwarder.address)
	if err != nil {
		return nil, err
	}
	if forwarder.transport == "udp" && response != nil && response.Truncated {
		client.Net = "tcp"
		return exchangeWithClient(ctx, client, request, forwarder.address)
	}
	return response, nil
}

func exchangeWithClient(ctx context.Context, client *mdns.Client, request *mdns.Msg, address string) (*mdns.Msg, error) {
	response, _, err := client.ExchangeContext(ctx, request.Copy(), address)
	return response, err
}
