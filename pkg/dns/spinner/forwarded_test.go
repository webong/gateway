package spinner

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	bridge "github.com/webong/gateway/arranger/integration"
)

func TestDNSForwardsQueriesForPermittedClients(t *testing.T) {
	address, calls := forwardedUDPServer(t, "198.51.100.42")
	observations := &captureObservationQueue{accepted: true}
	planner := plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("forwarded DNS query unexpectedly reached Planner")
		return bridge.GatewayDecision{}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:5353", Observations: observations, Forwarders: []Forwarder{{
		Name: "application", Address: address, Transport: "udp", Domains: []string{"."},
		ClientCIDRs: []string{"192.0.2.0/24"}, TimeoutMS: 500,
	}}}, planner, &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	allowed := exchangeHandler(server, "outside.example.", mdns.TypeA)
	if allowed == nil || allowed.Rcode != mdns.RcodeSuccess || len(allowed.Answer) != 1 {
		t.Fatalf("forwarded resolver answer missing: %+v", allowed)
	}
	answer, ok := allowed.Answer[0].(*mdns.A)
	if !ok || answer.A.String() != "198.51.100.42" || allowed.Authoritative || !allowed.RecursionAvailable {
		t.Fatalf("unexpected forwarded resolver answer: %+v", allowed)
	}
	if observations.last.Attributes["mode"] != "forwarded" || observations.last.Attributes["forwarder"] != "application" ||
		observations.last.Attributes["authoritative"] != "false" || observations.last.Attributes["answer_types"] != "A:1" {
		t.Fatalf("unexpected forwarded observation: %+v", observations.last)
	}

	deniedWriter := &responseWriter{
		local:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353},
		remote: &net.UDPAddr{IP: net.ParseIP("198.51.100.10"), Port: 53000},
	}
	query := new(mdns.Msg)
	query.SetQuestion("outside.example.", mdns.TypeA)
	server.ServeDNS(deniedWriter, query)
	if deniedWriter.message == nil || deniedWriter.message.Rcode != mdns.RcodeRefused || calls.Load() != 1 {
		t.Fatalf("unauthorized client reached forwarded resolver: response=%+v calls=%d", deniedWriter.message, calls.Load())
	}
}

func TestDNSManagedZoneTakesPrecedenceOverForwarder(t *testing.T) {
	address, calls := forwardedUDPServer(t, "198.51.100.42")
	planner := plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS,
			Action: bridge.GatewayRespond, Payload: []byte(`[{"type":"a","value":"192.0.2.44"}]`)}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:5353",
		Zones: []Zone{{Name: "managed.example", Nameservers: []string{"ns1.managed.example"}}},
		Forwarders: []Forwarder{{Name: "application", Address: address, Transport: "udp",
			Domains: []string{"."}, ClientCIDRs: []string{"192.0.2.0/24"}}},
	}, planner, &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	response := exchangeHandler(server, "payload.hook.managed.example.", mdns.TypeA)
	if response == nil || len(response.Answer) != 1 || response.Answer[0].(*mdns.A).A.String() != "192.0.2.44" {
		t.Fatalf("managed answer missing: %+v", response)
	}
	if calls.Load() != 0 {
		t.Fatalf("managed query leaked to forwarded resolver: calls=%d", calls.Load())
	}
}

func TestDNSForwarderFallsBackToTCPForTruncatedUDP(t *testing.T) {
	address, udpCalls, tcpCalls := forwardedUDPAndTCPServer(t, "203.0.113.7")
	server, err := NewServer(Config{Address: "127.0.0.1:5353", Forwarders: []Forwarder{{
		Name: "application", Address: address, Transport: "udp", Domains: []string{"example"},
		ClientCIDRs: []string{"192.0.2.0/24"}, TimeoutMS: 500,
	}}}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	response := exchangeHandler(server, "www.example.", mdns.TypeA)
	if response == nil || len(response.Answer) != 1 || response.Answer[0].(*mdns.A).A.String() != "203.0.113.7" {
		t.Fatalf("TCP fallback answer missing: %+v", response)
	}
	if udpCalls.Load() != 1 || tcpCalls.Load() != 1 {
		t.Fatalf("unexpected upstream calls: udp=%d tcp=%d", udpCalls.Load(), tcpCalls.Load())
	}
}

func TestDNSForwarderRejectsAmbiguousOrOpenPolicy(t *testing.T) {
	base := Forwarder{Name: "application", Address: "127.0.0.1:53", Transport: "udp",
		Domains: []string{"."}, ClientCIDRs: []string{"192.0.2.0/24"}}
	for _, test := range []struct {
		name       string
		forwarders []Forwarder
	}{
		{name: "open IPv4 network", forwarders: []Forwarder{{Name: "application", Address: "127.0.0.1:53",
			Transport: "udp", Domains: []string{"."}, ClientCIDRs: []string{"0.0.0.0/0"}}}},
		{name: "duplicate route", forwarders: []Forwarder{base, {Name: "other", Address: "127.0.0.2:53",
			Transport: "udp", Domains: []string{"."}, ClientCIDRs: []string{"192.0.2.0/24"}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := normalizeForwarders(test.forwarders); err == nil {
				t.Fatal("invalid forwarded DNS policy was accepted")
			}
		})
	}
}

func TestDNSForwarderSelectsMostSpecificDomainAndClient(t *testing.T) {
	routes, err := normalizeForwarders([]Forwarder{
		{Name: "default", Address: "127.0.0.1:5301", Domains: []string{"."}, ClientCIDRs: []string{"192.0.2.0/24"}},
		{Name: "application", Address: "127.0.0.1:5302", Domains: []string{"internal.example"}, ClientCIDRs: []string{"192.0.2.0/24"}},
		{Name: "tenant", Address: "127.0.0.1:5303", Domains: []string{"internal.example"}, ClientCIDRs: []string{"192.0.2.128/25"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{forwarders: routes}
	resolve := func(name, client string) string {
		t.Helper()
		selected := server.forwarderFor(canonicalName(name), &net.UDPAddr{IP: net.ParseIP(client), Port: 53000})
		if selected == nil {
			return ""
		}
		return selected.name
	}
	if selected := resolve("www.internal.example", "192.0.2.200"); selected != "tenant" {
		t.Fatalf("most-specific client policy not selected: %q", selected)
	}
	if selected := resolve("www.internal.example", "192.0.2.20"); selected != "application" {
		t.Fatalf("domain-specific policy not selected: %q", selected)
	}
	if selected := resolve("www.external.example", "192.0.2.20"); selected != "default" {
		t.Fatalf("root policy not selected: %q", selected)
	}
}

func forwardedUDPServer(t *testing.T, address string) (string, *atomic.Int32) {
	t.Helper()
	packet, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	server := &mdns.Server{PacketConn: packet, Handler: mdns.HandlerFunc(func(writer mdns.ResponseWriter, request *mdns.Msg) {
		calls.Add(1)
		response := new(mdns.Msg)
		response.SetReply(request)
		response.RecursionAvailable = true
		response.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: request.Question[0].Name,
			Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 30}, A: net.ParseIP(address).To4()}}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.ShutdownContext(context.Background()) })
	return packet.LocalAddr().String(), calls
}

func forwardedUDPAndTCPServer(t *testing.T, address string) (string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	packet, err := net.ListenPacket("udp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	udpCalls := &atomic.Int32{}
	tcpCalls := &atomic.Int32{}
	udp := &mdns.Server{PacketConn: packet, Handler: mdns.HandlerFunc(func(writer mdns.ResponseWriter, request *mdns.Msg) {
		udpCalls.Add(1)
		response := new(mdns.Msg)
		response.SetReply(request)
		response.Truncated = true
		_ = writer.WriteMsg(response)
	})}
	tcp := &mdns.Server{Listener: listener, Handler: mdns.HandlerFunc(func(writer mdns.ResponseWriter, request *mdns.Msg) {
		tcpCalls.Add(1)
		response := new(mdns.Msg)
		response.SetReply(request)
		response.RecursionAvailable = true
		response.Answer = []mdns.RR{&mdns.A{Hdr: mdns.RR_Header{Name: request.Question[0].Name,
			Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 30}, A: net.ParseIP(address).To4()}}
		_ = writer.WriteMsg(response)
	})}
	go func() { _ = udp.ActivateAndServe() }()
	go func() { _ = tcp.ActivateAndServe() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = udp.ShutdownContext(ctx)
		_ = tcp.ShutdownContext(ctx)
	})
	return listener.Addr().String(), udpCalls, tcpCalls
}
