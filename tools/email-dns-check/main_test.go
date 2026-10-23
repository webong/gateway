package main

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

type mxLookupFunc func(context.Context, string) ([]*net.MX, error)

func (f mxLookupFunc) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	return f(ctx, domain)
}

type spfCheckFunc func(context.Context, net.IP, string, string) string

func (f spfCheckFunc) Check(ctx context.Context, ip net.IP, domain, helo string) string {
	return f(ctx, ip, domain, helo)
}

func TestVerifyReturnDomainMXAndRelaySPF(t *testing.T) {
	lookup := mxLookupFunc(func(_ context.Context, domain string) ([]*net.MX, error) {
		if domain != "returns.example.test" {
			t.Fatalf("unexpected MX domain %q", domain)
		}
		return []*net.MX{{Host: "NS1.EXAMPLE.TEST."}, {Host: "ns2.example.test."}}, nil
	})
	checked := 0
	check := spfCheckFunc(func(_ context.Context, ip net.IP, domain, helo string) string {
		if domain != "returns.example.test" || helo != "ns1.example.test" || ip == nil {
			t.Fatalf("unexpected SPF inputs: %s %s %s", ip, domain, helo)
		}
		checked++
		return "pass"
	})
	if err := verify(context.Background(), "RETURNS.example.test.", []string{"ns1.example.test", "ns2.example.test"}, []string{"192.0.2.1", "2001:db8::1"}, lookup, check); err != nil {
		t.Fatal(err)
	}
	if checked != 2 {
		t.Fatalf("checked %d relay addresses, want 2", checked)
	}
}

func TestVerifyRejectsIncompleteOrUnsafeDNS(t *testing.T) {
	goodMX := mxLookupFunc(func(context.Context, string) ([]*net.MX, error) {
		return []*net.MX{{Host: "mx.example.test."}}, nil
	})
	pass := spfCheckFunc(func(context.Context, net.IP, string, string) string { return "pass" })
	tests := []struct {
		name     string
		domain   string
		hosts    []string
		ips      []string
		resolver mxResolver
		spf      spfEvaluator
		want     string
	}{
		{"missing domain", "", []string{"mx.example.test"}, []string{"192.0.2.1"}, goodMX, pass, "provide a valid"},
		{"missing MX", "returns.example.test", []string{"mx.example.test"}, []string{"192.0.2.1"}, mxLookupFunc(func(context.Context, string) ([]*net.MX, error) { return nil, nil }), pass, "no explicit MX"},
		{"unexpected MX", "returns.example.test", []string{"mx.example.test"}, []string{"192.0.2.1"}, mxLookupFunc(func(context.Context, string) ([]*net.MX, error) { return []*net.MX{{Host: "elsewhere.test."}}, nil }), pass, "unexpected MX"},
		{"missing configured MX", "returns.example.test", []string{"mx.example.test", "mx2.example.test"}, []string{"192.0.2.1"}, goodMX, pass, "missing expected MX"},
		{"private relay IP", "returns.example.test", []string{"mx.example.test"}, []string{"127.0.0.1"}, goodMX, pass, "not a public IP"},
		{"SPF fail", "returns.example.test", []string{"mx.example.test"}, []string{"192.0.2.1"}, goodMX, spfCheckFunc(func(context.Context, net.IP, string, string) string { return "fail" }), "does not authorize"},
		{"DNS error", "returns.example.test", []string{"mx.example.test"}, []string{"192.0.2.1"}, mxLookupFunc(func(context.Context, string) ([]*net.MX, error) { return nil, errors.New("dns down") }), pass, "lookup MX"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := verify(context.Background(), tc.domain, tc.hosts, tc.ips, tc.resolver, tc.spf)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("verify error = %v, want %q", err, tc.want)
			}
		})
	}
}
