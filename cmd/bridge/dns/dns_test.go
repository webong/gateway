package dns

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	bridge "github.com/webong/gateway/cmd/bridge"
)

func TestDNSQueryPlansAndBuildsImmediateAnswer(t *testing.T) {
	var planned bridge.GatewayEvent
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		planned = event
		return bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolDNS,
			Action:   bridge.GatewayRespond,
			Payload:  []byte(`[{"type":"a","value":"192.0.2.10"}]`),
		}, nil
	})
	server := newTestServer(t, planner, &recordingExecutor{})

	response := exchangeHandler(server, "aGVsbG8.hook-1.dns.example.test.", mdns.TypeA)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("unexpected DNS response: %+v", response)
	}
	address, ok := response.Answer[0].(*mdns.A)
	if !ok || address.A.String() != "192.0.2.10" {
		t.Fatalf("unexpected A answer: %v", response.Answer)
	}
	if planned.Protocol != bridge.ProtocolDNS || planned.Kind != bridge.EventQuery || planned.Route != "/hook-1" {
		t.Fatalf("unexpected planned event: %+v", planned)
	}
	if planned.Attributes["qtype"] != "A" || planned.Attributes["data"] != "aGVsbG8" {
		t.Fatalf("unexpected query attributes: %+v", planned.Attributes)
	}
	var payload Query
	if err := json.Unmarshal(planned.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Endpoint != "hook-1" || payload.Data != "aGVsbG8" {
		t.Fatalf("unexpected DNS payload: %+v", payload)
	}
}

func TestDNSQueriesReceiveUniqueEventIDs(t *testing.T) {
	var identifiers []string
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		identifiers = append(identifiers, event.ID)
		return bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolDNS,
			Action:   bridge.GatewayAccept,
		}, nil
	})
	server := newTestServer(t, planner, &recordingExecutor{})

	exchangeHandler(server, "same.hook-1.dns.example.test.", mdns.TypeTXT)
	exchangeHandler(server, "same.hook-1.dns.example.test.", mdns.TypeTXT)

	if len(identifiers) != 2 || identifiers[0] == identifiers[1] {
		t.Fatalf("expected distinct event IDs, got %v", identifiers)
	}
}

func TestDNSQueryUsesSynchronousReplyAndQueuesRelays(t *testing.T) {
	executor := &recordingExecutor{
		response: bridge.Response{StatusCode: 200, Body: []byte(`[{"type":"txt","value":"hello\nworld"}]`)},
	}
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version:  bridge.ProtocolVersion,
			Protocol: bridge.ProtocolDNS,
			Action:   bridge.GatewayDeliver,
			Reply: &bridge.GatewayDelivery{
				Protocol: bridge.ProtocolHTTP,
				Target:   "https://reply.example.test/dns",
			},
			Deliveries: []bridge.GatewayDelivery{{
				Protocol: bridge.ProtocolHTTP,
				Target:   "https://relay.example.test/dns",
			}},
		}, nil
	})
	server := newTestServer(t, planner, executor)

	response := exchangeHandler(server, "payload.hook-2.dns.example.test.", mdns.TypeTXT)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("unexpected DNS response: %+v", response)
	}
	txt, ok := response.Answer[0].(*mdns.TXT)
	if !ok || len(txt.Txt) != 2 || txt.Txt[0] != "hello" || txt.Txt[1] != "world" {
		t.Fatalf("unexpected TXT answer: %v", response.Answer)
	}
	if executor.delivered.URL != "https://reply.example.test/dns" || len(executor.delivered.Body) == 0 {
		t.Fatalf("unexpected synchronous delivery: %+v", executor.delivered)
	}
	if len(executor.enqueued) != 1 || executor.enqueued[0].Target != "https://relay.example.test/dns" {
		t.Fatalf("unexpected relays: %+v", executor.enqueued)
	}
}

func TestDNSRejectsUnknownRoutesAndRefusesRecursionOrAny(t *testing.T) {
	plans := 0
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		plans++
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS, Action: bridge.GatewayReject}, nil
	})
	server := newTestServer(t, planner, &recordingExecutor{})

	unknown := exchangeHandler(server, "payload.hook-3.dns.example.test.", mdns.TypeA)
	if unknown.Rcode != mdns.RcodeNameError || len(unknown.Ns) != 1 {
		t.Fatalf("expected authoritative NXDOMAIN, got %+v", unknown)
	}
	outside := exchangeHandler(server, "outside.example.test.", mdns.TypeA)
	if outside.Rcode != mdns.RcodeRefused {
		t.Fatalf("expected outside-zone refusal, got %s", mdns.RcodeToString[outside.Rcode])
	}
	any := exchangeHandler(server, "payload.hook-3.dns.example.test.", mdns.TypeANY)
	if any.Rcode != mdns.RcodeRefused {
		t.Fatalf("expected ANY refusal, got %s", mdns.RcodeToString[any.Rcode])
	}
	if plans != 1 {
		t.Fatalf("expected only the valid in-zone query to reach PHP, got %d", plans)
	}
}

func TestDNSAnswersZoneAuthorityWithoutPlanning(t *testing.T) {
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		t.Fatal("zone authority query must not reach the planner")
		return bridge.GatewayDecision{}, nil
	})
	server := newTestServer(t, planner, &recordingExecutor{})

	response := exchangeHandler(server, "dns.example.test.", mdns.TypeNS)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("unexpected NS response: %+v", response)
	}
	nameServer, ok := response.Answer[0].(*mdns.NS)
	if !ok || nameServer.Ns != "ns1.example.test." {
		t.Fatalf("unexpected NS answer: %v", response.Answer)
	}
}

func TestDNSInvalidDynamicResponseReturnsServerFailure(t *testing.T) {
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS, Action: bridge.GatewayRespond,
			Payload: []byte(`[{"type":"a","value":"not-an-address"}]`),
		}, nil
	})
	server := newTestServer(t, planner, &recordingExecutor{})

	response := exchangeHandler(server, "payload.hook.dns.example.test.", mdns.TypeA)
	if response.Rcode != mdns.RcodeServerFailure {
		t.Fatalf("expected SERVFAIL for an invalid dynamic response, got %+v", response)
	}
}

func TestDNSRateLimitDropsExcessQueriesBeforePlanning(t *testing.T) {
	plans := 0
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		plans++
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS, Action: bridge.GatewayAccept}, nil
	})
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", Zone: "dns.example.test", Nameservers: []string{"ns1.example.test"},
		RateLimit: 1, RateBurst: 1,
	}, planner, &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	first := exchangeHandler(server, "first.hook.dns.example.test.", mdns.TypeA)
	second := exchangeHandler(server, "second.hook.dns.example.test.", mdns.TypeA)
	if first == nil || second != nil || plans != 1 {
		t.Fatalf("expected one answer and one rate-limited drop, first=%+v second=%+v plans=%d", first, second, plans)
	}
}

func TestDNSServerServesUDPAndTCP(t *testing.T) {
	address := availableAddress(t)
	planner := plannerFunc(func(_ context.Context, _ bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{
			Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolDNS, Action: bridge.GatewayRespond,
			Payload: []byte(`[{"type":"a","value":"192.0.2.20"}]`),
		}, nil
	})
	server, err := NewServer(Config{
		Address: address, Zone: "dns.example.test", Nameservers: []string{"ns1.example.test"},
	}, planner, &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- server.ListenAndServe() }()
	for _, network := range []string{"udp", "tcp"} {
		client := &mdns.Client{Net: network, Timeout: 200 * time.Millisecond}
		var response *mdns.Msg
		for attempt := 0; attempt < 20; attempt++ {
			query := new(mdns.Msg)
			query.SetQuestion("payload.hook.dns.example.test.", mdns.TypeA)
			response, _, err = client.Exchange(query, address)
			if err == nil {
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
		if err != nil || response == nil || len(response.Answer) != 1 {
			t.Fatalf("%s DNS exchange failed: response=%+v err=%v", network, response, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-serveResult; !errors.Is(err, ErrServerClosed) {
		t.Fatalf("expected graceful DNS shutdown, got %v", err)
	}
}

func newTestServer(t *testing.T, planner bridge.ProtocolPlanner, executor Executor) *Server {
	t.Helper()
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", Zone: "dns.example.test", Nameservers: []string{"ns1.example.test"},
	}, planner, executor)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func exchangeHandler(server *Server, name string, queryType uint16) *mdns.Msg {
	request := new(mdns.Msg)
	request.SetQuestion(name, queryType)
	writer := &responseWriter{
		local:  &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353},
		remote: &net.UDPAddr{IP: net.ParseIP("192.0.2.100"), Port: 53000},
	}
	server.ServeDNS(writer, request)
	return writer.message
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

type plannerFunc func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error)

func (function plannerFunc) PlanEvent(ctx context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
	return function(ctx, event)
}

type recordingExecutor struct {
	mu        sync.Mutex
	delivered bridge.Delivery
	enqueued  []bridge.GatewayDelivery
	response  bridge.Response
	err       error
}

func (executor *recordingExecutor) Deliver(_ context.Context, delivery bridge.Delivery) (bridge.Response, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.delivered = delivery
	return executor.response, executor.err
}

func (executor *recordingExecutor) EnqueueGateway(delivery bridge.GatewayDelivery) bool {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	executor.enqueued = append(executor.enqueued, delivery)
	return true
}

type responseWriter struct {
	message *mdns.Msg
	local   net.Addr
	remote  net.Addr
}

func (writer *responseWriter) LocalAddr() net.Addr  { return writer.local }
func (writer *responseWriter) RemoteAddr() net.Addr { return writer.remote }
func (writer *responseWriter) WriteMsg(message *mdns.Msg) error {
	writer.message = message.Copy()
	return nil
}
func (writer *responseWriter) Write(_ []byte) (int, error) { return 0, nil }
func (writer *responseWriter) Close() error                { return nil }
func (writer *responseWriter) TsigStatus() error           { return nil }
func (writer *responseWriter) TsigTimersOnly(bool)         {}
func (writer *responseWriter) Hijack()                     {}
