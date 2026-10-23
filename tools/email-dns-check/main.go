// email-dns-check verifies the public DNS prerequisites for a Gateway return
// domain before an operator enables SRS rewriting. It does not send mail.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/wttw/spf"
)

type mxResolver interface {
	LookupMX(context.Context, string) ([]*net.MX, error)
}

type spfEvaluator interface {
	Check(context.Context, net.IP, string, string) string
}

type publicSPF struct{}

func (publicSPF) Check(ctx context.Context, ip net.IP, domain, helo string) string {
	return spf.NewChecker().CheckHost(ctx, ip, dns.Fqdn(domain), "postmaster@"+domain, helo).Type.String()
}

func main() {
	domain := flag.String("domain", "", "Gateway-controlled SRS return domain")
	mxHosts := flag.String("mx-hosts", "", "comma-separated MX hosts that route to Gateway")
	relayIPs := flag.String("relay-ips", "", "comma-separated public IPs used by the outbound relay")
	timeout := flag.Duration("timeout", 10*time.Second, "maximum time for DNS verification")
	flag.Parse()
	if *timeout <= 0 {
		fail(errors.New("timeout must be positive"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := verify(ctx, *domain, splitList(*mxHosts), splitList(*relayIPs), net.DefaultResolver, publicSPF{}); err != nil {
		fail(err)
	}
	fmt.Printf("Return-domain MX and outbound-relay SPF checks passed for %s.\n", canonical(*domain))
}

func verify(ctx context.Context, domain string, expectedMX, relayAddresses []string, resolver mxResolver, evaluator spfEvaluator) error {
	domain = canonical(domain)
	if !validDomain(domain) || len(expectedMX) == 0 || len(relayAddresses) == 0 {
		return errors.New("provide a valid -domain, -mx-hosts, and -relay-ips")
	}
	if resolver == nil || evaluator == nil {
		return errors.New("DNS and SPF evaluators are required")
	}
	want := make(map[string]bool, len(expectedMX))
	for _, host := range expectedMX {
		host = canonical(host)
		if !validDomain(host) {
			return fmt.Errorf("invalid expected MX host %q", host)
		}
		want[host] = false
	}
	mxRecords, err := resolver.LookupMX(ctx, domain)
	if err != nil {
		return fmt.Errorf("lookup MX for %s: %w", domain, err)
	}
	if len(mxRecords) == 0 {
		return fmt.Errorf("%s has no explicit MX records", domain)
	}
	for _, record := range mxRecords {
		if record == nil {
			return fmt.Errorf("%s returned an invalid MX record", domain)
		}
		host := canonical(record.Host)
		if _, ok := want[host]; !ok {
			return fmt.Errorf("%s has unexpected MX host %q", domain, host)
		}
		want[host] = true
	}
	for host, seen := range want {
		if !seen {
			return fmt.Errorf("%s is missing expected MX host %q", domain, host)
		}
	}
	for _, address := range relayAddresses {
		ip := net.ParseIP(address)
		if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return fmt.Errorf("outbound relay address %q is not a public IP", address)
		}
		result := evaluator.Check(ctx, ip, domain, canonical(expectedMX[0]))
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("SPF lookup for %s: %w", domain, err)
		}
		if result != "pass" {
			return fmt.Errorf("SPF for %s does not authorize outbound relay IP %s (result: %s)", domain, address, result)
		}
	}
	return nil
}

func splitList(raw string) []string {
	items := []string{}
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			items = append(items, trimmed)
		}
	}
	return items
}

func canonical(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func validDomain(value string) bool {
	if value == "" || len(value) > 253 || !strings.Contains(value, ".") {
		return false
	}
	_, ok := dns.IsDomainName(dns.Fqdn(value))
	return ok
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
