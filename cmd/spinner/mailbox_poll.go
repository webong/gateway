package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	emailruntime "github.com/webong/gateway/pkg/email/spinner"
)

type mailboxCallback struct {
	resolver *emailruntime.DNSResolver
	private  []netip.Prefix
	timeout  time.Duration
}

func newMailboxCallback(resolver *emailruntime.DNSResolver, privateCIDRs []string, timeout time.Duration) (*mailboxCallback, error) {
	if resolver == nil {
		return nil, errors.New("mailbox callback requires Gateway DNS")
	}
	private := make([]netip.Prefix, 0, len(privateCIDRs))
	for _, raw := range privateCIDRs {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || prefix != prefix.Masked() || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid mailbox callback CIDR %q", raw)
		}
		private = append(private, prefix)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &mailboxCallback{resolver: resolver, private: private, timeout: timeout}, nil
}

func (callback *mailboxCallback) Deliver(ctx context.Context, target string, headers http.Header, body []byte) (int, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return 0, errors.New("invalid mailbox callback URL")
	}
	transport := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		ResponseHeaderTimeout: callback.timeout,
		IdleConnTimeout:       30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := callback.resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			var last error
			for _, candidate := range addresses {
				value, ok := netip.AddrFromSlice(candidate.IP)
				if !ok || !callback.allowed(value.Unmap()) {
					continue
				}
				connection, dialErr := (&net.Dialer{Timeout: callback.timeout}).DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				last = dialErr
			}
			if last != nil {
				return nil, last
			}
			return nil, errors.New("mailbox callback resolves only to blocked addresses")
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header = headers.Clone()
	client := &http.Client{
		Transport:     transport,
		Timeout:       callback.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}

func (callback *mailboxCallback) allowed(address netip.Addr) bool {
	if address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() &&
		!address.IsLinkLocalUnicast() && !address.IsUnspecified() {
		return true
	}
	for _, prefix := range callback.private {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
