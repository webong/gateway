package dns

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	mdns "github.com/miekg/dns"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
)

func TestServerReplacesZonesAtomically(t *testing.T) {
	server, err := NewServer(Config{
		Address: "127.0.0.1:5353", AllowEmptyZones: true,
	}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	if response := exchangeHandler(server, "hook.dynamic.example.test.", mdns.TypeA); response.Rcode != mdns.RcodeRefused {
		t.Fatalf("empty snapshot should refuse queries: %+v", response)
	}
	if err := server.ReplaceZones([]Zone{{Name: "dynamic.example.test", Nameservers: []string{"ns1.example.test"}, TTL: 15}}); err != nil {
		t.Fatal(err)
	}
	authority := exchangeHandler(server, "dynamic.example.test.", mdns.TypeNS)
	if authority.Rcode != mdns.RcodeSuccess || len(authority.Answer) != 1 || authority.Answer[0].Header().Ttl != 15 {
		t.Fatalf("replacement snapshot was not served: %+v", authority)
	}
	if err := server.ReplaceZones([]Zone{{Name: "bad..example.test", Nameservers: []string{"ns1.example.test"}}}); err == nil {
		t.Fatal("invalid snapshot was accepted")
	}
	if response := exchangeHandler(server, "dynamic.example.test.", mdns.TypeNS); response.Rcode != mdns.RcodeSuccess {
		t.Fatalf("invalid snapshot replaced the last valid one: %+v", response)
	}
	if err := server.ReplaceZones(nil); err != nil {
		t.Fatal(err)
	}
	if response := exchangeHandler(server, "dynamic.example.test.", mdns.TypeNS); response.Rcode != mdns.RcodeRefused {
		t.Fatalf("empty replacement did not remove authority: %+v", response)
	}
}

func TestHTTPZoneSourceAuthenticatesAndRefreshesServer(t *testing.T) {
	var receivedToken string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedToken = request.Header.Get("X-Gateway-Internal")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"name":"api.example.test","nameservers":["ns1.example.test"],"ttl":30}]}`)),
			Request:    request,
		}, nil
	})}

	source, err := NewHTTPZoneSource("http://planner.internal", "internal-secret", client)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(Config{Address: "127.0.0.1:5353", AllowEmptyZones: true},
		plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			return bridge.GatewayDecision{}, nil
		}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (ZoneReloader{Server: server, Source: source}).Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if receivedToken != "internal-secret" {
		t.Fatalf("zone source did not authenticate: %q", receivedToken)
	}
	response := exchangeHandler(server, "api.example.test.", mdns.TypeNS)
	if response.Rcode != mdns.RcodeSuccess || len(response.Answer) != 1 {
		t.Fatalf("refreshed zone was not served: %+v", response)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
