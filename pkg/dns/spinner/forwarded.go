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

var forwardedResolverName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// ForwardedResolver is one upstream DNS answer source. Domains and client
// networks form an explicit forwarding policy; no implicit catch-all is
// created when the list is empty.
type ForwardedResolver struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Transport   string   `json:"transport"`
	Domains     []string `json:"domains"`
	ClientCIDRs []string `json:"client_cidrs"`
	TimeoutMS   int      `json:"timeout_ms,omitempty"`
}

type forwardedRoute struct {
	name      string
	address   string
	transport string
	domain    string
	client    netip.Prefix
	timeout   time.Duration
}

func normalizeForwardedResolvers(resolvers []ForwardedResolver) ([]forwardedRoute, error) {
	if len(resolvers) > 32 {
		return nil, fmt.Errorf("DNS forwarded resolver count exceeds 32")
	}
	routes := make([]forwardedRoute, 0)
	seenNames := make(map[string]bool, len(resolvers))
	seenRoutes := make(map[string]bool)
	for _, resolver := range resolvers {
		resolver.Name = strings.ToLower(strings.TrimSpace(resolver.Name))
		if !forwardedResolverName.MatchString(resolver.Name) || seenNames[resolver.Name] {
			return nil, fmt.Errorf("invalid or duplicate DNS forwarded resolver name %q", resolver.Name)
		}
		seenNames[resolver.Name] = true
		if err := validateForwardedAddress(resolver.Address); err != nil {
			return nil, fmt.Errorf("DNS forwarded resolver %s: %w", resolver.Name, err)
		}
		resolver.Transport = strings.ToLower(strings.TrimSpace(resolver.Transport))
		if resolver.Transport == "" {
			resolver.Transport = "udp"
		}
		if resolver.Transport != "udp" && resolver.Transport != "tcp" {
			return nil, fmt.Errorf("DNS forwarded resolver %s has unsupported transport %q", resolver.Name, resolver.Transport)
		}
		if len(resolver.Domains) == 0 || len(resolver.Domains) > 32 || len(resolver.ClientCIDRs) == 0 || len(resolver.ClientCIDRs) > 32 {
			return nil, fmt.Errorf("DNS forwarded resolver %s requires 1 to 32 domains and client networks", resolver.Name)
		}
		timeout := defaultForwardedTimeout
		if resolver.TimeoutMS != 0 {
			if resolver.TimeoutMS < 10 || resolver.TimeoutMS > 5000 {
				return nil, fmt.Errorf("DNS forwarded resolver %s timeout must be between 10 and 5000 milliseconds", resolver.Name)
			}
			timeout = time.Duration(resolver.TimeoutMS) * time.Millisecond
		}

		domains := make([]string, 0, len(resolver.Domains))
		for _, value := range resolver.Domains {
			domain := canonicalName(value)
			if domain != "." && !validAuthorityName(domain) {
				return nil, fmt.Errorf("DNS forwarded resolver %s has invalid domain %q", resolver.Name, value)
			}
			domains = append(domains, domain)
		}
		clients := make([]netip.Prefix, 0, len(resolver.ClientCIDRs))
		for _, value := range resolver.ClientCIDRs {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || prefix != prefix.Masked() || prefix.Addr().IsUnspecified() {
				return nil, fmt.Errorf("DNS forwarded resolver %s has invalid client network %q", resolver.Name, value)
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
				routes = append(routes, forwardedRoute{name: resolver.Name, address: resolver.Address,
					transport: resolver.Transport, domain: domain, client: client, timeout: timeout})
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

func (s *Server) forwardedResolverFor(name string, remote net.Addr) *forwardedRoute {
	client, ok := forwardedClientAddress(remote)
	if !ok {
		return nil
	}
	for index := range s.forwarded {
		route := &s.forwarded[index]
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

func (s *Server) forwardQuery(writer mdns.ResponseWriter, request *mdns.Msg, resolver forwardedRoute, observed *dnsObservation) {
	ctx, cancel := context.WithTimeout(context.Background(), resolver.timeout)
	defer cancel()
	response, err := exchangeForwarded(ctx, request, resolver)
	if err != nil || response == nil {
		response = new(mdns.Msg)
		response.SetRcode(request, mdns.RcodeServerFailure)
		observed.failure = "forwarded_upstream"
	} else {
		response.Id = request.Id
	}
	s.writeObservedResponse(writer, request, response, observed)
}

func exchangeForwarded(ctx context.Context, request *mdns.Msg, resolver forwardedRoute) (*mdns.Msg, error) {
	client := &mdns.Client{Net: resolver.transport, Timeout: resolver.timeout}
	response, _, err := client.ExchangeContext(ctx, request.Copy(), resolver.address)
	if err != nil {
		return nil, err
	}
	if resolver.transport == "udp" && response != nil && response.Truncated {
		client.Net = "tcp"
		return exchangeWithClient(ctx, client, request, resolver.address)
	}
	return response, nil
}

func exchangeWithClient(ctx context.Context, client *mdns.Client, request *mdns.Msg, address string) (*mdns.Msg, error) {
	response, _, err := client.ExchangeContext(ctx, request.Copy(), address)
	return response, err
}
