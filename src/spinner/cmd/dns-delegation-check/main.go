// dns-delegation-check verifies a deployed Gateway authoritative zone without
// modifying DNS or Gateway state. It does not replace multi-network probes.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/miekg/dns"
)

type checkConfig struct {
	zone          string
	parent        string
	parentZone    string
	resolver      string
	nodes         []string
	nameservers   []string
	probe         string
	negativeProbe string
	probeType     uint16
	allowPrivate  bool
}

type exchanger func(context.Context, string, string, string, uint16) (*dns.Msg, error)
type addressLookup func(context.Context, string, string) ([]net.IP, error)

func main() {
	zone := flag.String("zone", "", "delegated Gateway zone")
	parent := flag.String("parent", "", "parent-zone authoritative IP, optionally with port")
	parentZone := flag.String("parent-zone", "", "zone served authoritatively by the parent DNS server")
	resolver := flag.String("resolver", "", "recursive DNS server IP used to resolve nameserver addresses")
	nodes := flag.String("nodes", "", "comma-separated authoritative node IPs, in nameserver order, optionally with ports")
	nameservers := flag.String("nameservers", "", "comma-separated expected NS hostnames, in node order")
	probe := flag.String("probe", "", "registered hook query name (answer or NODATA)")
	negativeProbe := flag.String("negative-probe", "", "known unregistered hook query name inside the same zone")
	probeType := flag.String("probe-type", "TXT", "record type expected from the hook query")
	timeout := flag.Duration("timeout", 15*time.Second, "total verification deadline")
	allowPrivate := flag.Bool("allow-private", false, "allow private and loopback IPs for local tests only")
	flag.Parse()
	if *timeout <= 0 {
		fail(errors.New("timeout must be positive"))
	}
	recordType, ok := dns.StringToType[strings.ToUpper(strings.TrimSpace(*probeType))]
	if !ok || recordType == dns.TypeANY || recordType == dns.TypeNS || recordType == dns.TypeSOA {
		fail(fmt.Errorf("unsupported probe type %q", *probeType))
	}
	config := checkConfig{
		zone: *zone, parent: *parent, parentZone: *parentZone, resolver: *resolver, nodes: splitList(*nodes), nameservers: splitList(*nameservers),
		probe: *probe, negativeProbe: *negativeProbe, probeType: recordType, allowPrivate: *allowPrivate,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := verify(ctx, config, exchangeDNS, lookupNameserverIPs); err != nil {
		fail(err)
	}
	fmt.Printf("Parent delegation, nameserver addresses, authoritative UDP/TCP nodes, SOA consistency, and registered/unregistered hook checks passed for %s\n", canonical(*zone))
}

func verify(ctx context.Context, config checkConfig, exchange exchanger, lookup addressLookup) error {
	if exchange == nil || lookup == nil {
		return errors.New("DNS exchanger and address lookup are required")
	}
	zone := canonical(config.zone)
	parentZone := canonical(config.parentZone)
	probe := canonical(config.probe)
	negativeProbe := canonical(config.negativeProbe)
	if !validDomain(zone) || !validDomain(probe) || !validDomain(negativeProbe) || probe == zone || negativeProbe == zone || negativeProbe == probe || !dns.IsSubDomain(zone, probe) || !dns.IsSubDomain(zone, negativeProbe) {
		return errors.New("provide distinct registered and unregistered hook probes inside the zone")
	}
	if (!validDomain(parentZone) && parentZone != ".") || parentZone == zone || !dns.IsSubDomain(parentZone, zone) {
		return errors.New("provide a valid parent zone above the delegated zone")
	}
	if len(config.nodes) < 2 || len(config.nodes) != len(config.nameservers) {
		return errors.New("provide at least two node addresses and the same number of nameservers, in matching order")
	}
	if config.probeType == 0 || config.probeType == dns.TypeANY || config.probeType == dns.TypeNS || config.probeType == dns.TypeSOA {
		return errors.New("provide a supported hook probe record type")
	}
	parent, err := serverAddress(config.parent, config.allowPrivate)
	if err != nil {
		return fmt.Errorf("parent: %w", err)
	}
	resolver, err := serverAddress(config.resolver, config.allowPrivate)
	if err != nil {
		return fmt.Errorf("resolver: %w", err)
	}
	nodes := make([]string, 0, len(config.nodes))
	seenAddresses := map[string]bool{}
	seenIPs := map[string]bool{}
	for _, value := range config.nodes {
		address, err := serverAddress(value, config.allowPrivate)
		if err != nil {
			return fmt.Errorf("node %q: %w", value, err)
		}
		host, _, _ := net.SplitHostPort(address)
		if seenAddresses[address] || (!config.allowPrivate && seenIPs[host]) {
			return fmt.Errorf("duplicate authoritative node %q", value)
		}
		seenAddresses[address], seenIPs[host] = true, true
		nodes = append(nodes, address)
	}
	want := make([]string, 0, len(config.nameservers))
	for _, value := range config.nameservers {
		name := canonical(value)
		if !validDomain(name) || slices.Contains(want, name) {
			return fmt.Errorf("invalid or duplicate nameserver %q", value)
		}
		want = append(want, name)
	}
	slices.Sort(want)
	parentSOA, err := exchange(ctx, parent, "udp", parentZone, dns.TypeSOA)
	if err != nil {
		return fmt.Errorf("parent zone SOA query: %w", err)
	}
	if parentSOA != nil && parentSOA.Truncated {
		parentSOA, err = exchange(ctx, parent, "tcp", parentZone, dns.TypeSOA)
		if err != nil {
			return fmt.Errorf("parent zone SOA TCP retry: %w", err)
		}
	}
	if !authoritative(parentSOA) || soaValue(parentSOA, parentZone) == "" {
		return fmt.Errorf("parent %s is not authoritative for %s", parent, parentZone)
	}
	for i, value := range config.nameservers {
		name := canonical(value)
		addresses, err := lookup(ctx, resolver, name)
		if err != nil {
			return fmt.Errorf("nameserver %s address lookup: %w", name, err)
		}
		host, _, _ := net.SplitHostPort(nodes[i])
		nodeIP := net.ParseIP(host)
		matched := false
		for _, address := range addresses {
			if address.Equal(nodeIP) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("nameserver %s does not resolve to configured node %s", name, nodeIP)
		}
	}
	parentReply, err := exchange(ctx, parent, "udp", zone, dns.TypeNS)
	if err != nil {
		return fmt.Errorf("parent delegation query: %w", err)
	}
	if parentReply != nil && parentReply.Truncated {
		parentReply, err = exchange(ctx, parent, "tcp", zone, dns.TypeNS)
		if err != nil {
			return fmt.Errorf("parent delegation TCP retry: %w", err)
		}
	}
	if parentReply == nil || parentReply.Truncated || parentReply.Authoritative || parentReply.Rcode != dns.RcodeSuccess || !slices.Equal(nsNames(append(append([]dns.RR{}, parentReply.Answer...), parentReply.Ns...), zone), want) {
		return fmt.Errorf("parent %s does not delegate %s to the expected nameservers", parent, zone)
	}
	var expectedSOA string
	for _, node := range nodes {
		for _, network := range []string{"udp", "tcp"} {
			nsReply, err := exchange(ctx, node, network, zone, dns.TypeNS)
			if err != nil {
				return fmt.Errorf("%s %s NS query: %w", node, network, err)
			}
			if !authoritative(nsReply) || !slices.Equal(nsNames(nsReply.Answer, zone), want) {
				return fmt.Errorf("%s does not answer authoritative %s NS for %s", node, network, zone)
			}
			soaReply, err := exchange(ctx, node, network, zone, dns.TypeSOA)
			if err != nil {
				return fmt.Errorf("%s %s SOA query: %w", node, network, err)
			}
			soa := soaValue(soaReply, zone)
			if !authoritative(soaReply) || soa == "" {
				return fmt.Errorf("%s does not answer authoritative %s SOA for %s", node, network, zone)
			}
			if expectedSOA == "" {
				expectedSOA = soa
			} else if soa != expectedSOA {
				return fmt.Errorf("%s %s SOA differs from the first authoritative answer", node, network)
			}
			probeReply, err := exchange(ctx, node, network, probe, config.probeType)
			if err != nil {
				return fmt.Errorf("%s %s hook query: %w", node, network, err)
			}
			if !authoritative(probeReply) || (!hasRecord(probeReply.Answer, probe, config.probeType) && !hasSOA(probeReply.Ns, zone)) {
				return fmt.Errorf("%s does not return an authoritative registered-hook %s answer or NODATA over %s", node, dns.TypeToString[config.probeType], network)
			}
			negativeReply, err := exchange(ctx, node, network, negativeProbe, config.probeType)
			if err != nil {
				return fmt.Errorf("%s %s unregistered-hook query: %w", node, network, err)
			}
			if negativeReply == nil || negativeReply.Rcode != dns.RcodeNameError || !negativeReply.Authoritative || negativeReply.Truncated || !hasSOA(negativeReply.Ns, zone) {
				return fmt.Errorf("%s does not reject the unregistered hook with authoritative NXDOMAIN over %s", node, network)
			}
		}
	}
	return nil
}

func exchangeDNS(ctx context.Context, server, network, name string, recordType uint16) (*dns.Msg, error) {
	return exchangeMessage(ctx, server, network, name, recordType, false)
}

func lookupNameserverIPs(ctx context.Context, resolver, name string) ([]net.IP, error) {
	addresses := []net.IP{}
	for _, recordType := range []uint16{dns.TypeA, dns.TypeAAAA} {
		response, err := exchangeMessage(ctx, resolver, "udp", name, recordType, true)
		if err != nil {
			return nil, err
		}
		if response.Truncated {
			response, err = exchangeMessage(ctx, resolver, "tcp", name, recordType, true)
			if err != nil {
				return nil, err
			}
		}
		if response.Truncated || response.Rcode != dns.RcodeSuccess || !response.RecursionAvailable {
			return nil, fmt.Errorf("recursive %s lookup failed: %s", dns.TypeToString[recordType], dns.RcodeToString[response.Rcode])
		}
		for _, record := range response.Answer {
			if canonical(record.Header().Name) != name || record.Header().Rrtype != recordType {
				continue
			}
			switch value := record.(type) {
			case *dns.A:
				addresses = append(addresses, value.A)
			case *dns.AAAA:
				addresses = append(addresses, value.AAAA)
			}
		}
	}
	return addresses, nil
}

func exchangeMessage(ctx context.Context, server, network, name string, recordType uint16, recursive bool) (*dns.Msg, error) {
	message := new(dns.Msg)
	message.SetQuestion(name, recordType)
	message.RecursionDesired = recursive
	client := &dns.Client{Net: network, Timeout: 3 * time.Second}
	response, _, err := client.ExchangeContext(ctx, message, server)
	if err != nil {
		return nil, err
	}
	if response == nil || !response.Response || response.Id != message.Id || len(response.Question) != 1 || response.Question[0] != message.Question[0] {
		return nil, errors.New("DNS server returned a mismatched response")
	}
	return response, nil
}

func authoritative(message *dns.Msg) bool {
	return message != nil && message.Rcode == dns.RcodeSuccess && message.Authoritative && !message.Truncated
}

func nsNames(records []dns.RR, zone string) []string {
	names := []string{}
	for _, record := range records {
		value, ok := record.(*dns.NS)
		if !ok || canonical(value.Hdr.Name) != zone {
			continue
		}
		name := canonical(value.Ns)
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func soaValue(message *dns.Msg, zone string) string {
	if message == nil {
		return ""
	}
	for _, record := range message.Answer {
		if value, ok := record.(*dns.SOA); ok && canonical(value.Hdr.Name) == zone {
			return value.String()
		}
	}
	return ""
}

func hasRecord(records []dns.RR, name string, recordType uint16) bool {
	for _, record := range records {
		if record.Header().Rrtype == recordType && canonical(record.Header().Name) == name {
			return true
		}
	}
	return false
}

func hasSOA(records []dns.RR, zone string) bool {
	for _, record := range records {
		if record.Header().Rrtype == dns.TypeSOA && canonical(record.Header().Name) == zone {
			return true
		}
	}
	return false
}

func serverAddress(value string, allowPrivate bool) (string, error) {
	value = strings.TrimSpace(value)
	host, port := value, "53"
	if net.ParseIP(value) == nil {
		var err error
		host, port, err = net.SplitHostPort(value)
		if err != nil {
			return "", errors.New("provide a numeric IP address, optionally with a port")
		}
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("DNS server must be a numeric IP address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", errors.New("DNS server port must be between 1 and 65535")
	}
	if !allowPrivate && (!ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return "", errors.New("public deployment check requires a public server IP")
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(portNumber)), nil
}

func canonical(value string) string {
	return dns.Fqdn(strings.ToLower(strings.TrimSpace(value)))
}

func validDomain(value string) bool {
	if value == "." || len(value) > 254 {
		return false
	}
	_, ok := dns.IsDomainName(value)
	return ok
}

func splitList(value string) []string {
	items := []string{}
	for _, item := range strings.Split(value, ",") {
		if value := strings.TrimSpace(item); value != "" {
			items = append(items, value)
		}
	}
	return items
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
