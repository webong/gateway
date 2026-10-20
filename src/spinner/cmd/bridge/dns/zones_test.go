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

func TestPlannerZoneRecordsAnswerApexMXAndMailAuthenticationWithoutHookPlanning(t *testing.T) {
	called := false
	server, err := NewServer(Config{Address: "127.0.0.1:5353", Zones: []Zone{{
		Name: "sosongo.com", Nameservers: []string{"ns1.gateway.test"}, TTL: 60,
		Records: []ZoneRecord{
			{Name: "sosongo.com", Type: "MX", Value: "route1.mx.cloudflare.net", Preference: ptrPreference(10)},
			{Name: "sosongo.com", Type: "TXT", Value: "v=spf1 include:_spf.mx.cloudflare.net ~all"},
			{Name: "cf2024-1._domainkey.sosongo.com", Type: "TXT", Value: "v=DKIM1; p=abc"},
		},
	}}}, plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		called = true
		return bridge.GatewayDecision{}, nil
	}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	mx := exchangeHandler(server, "sosongo.com.", mdns.TypeMX)
	if mx.Rcode != mdns.RcodeSuccess || len(mx.Answer) != 1 || mx.Answer[0].(*mdns.MX).Mx != "route1.mx.cloudflare.net." {
		t.Fatalf("apex MX missing: %+v", mx)
	}
	txt := exchangeHandler(server, "cf2024-1._domainkey.sosongo.com.", mdns.TypeTXT)
	if txt.Rcode != mdns.RcodeSuccess || len(txt.Answer) != 1 {
		t.Fatalf("DKIM TXT missing: %+v", txt)
	}
	noData := exchangeHandler(server, "sosongo.com.", mdns.TypeA)
	if noData.Rcode != mdns.RcodeSuccess || len(noData.Answer) != 0 || len(noData.Ns) == 0 {
		t.Fatalf("existing owner should return NODATA: %+v", noData)
	}
	if called {
		t.Fatal("static DNS records unexpectedly invoked a dynamic hook")
	}
	if err := server.ReplaceZones([]Zone{{Name: "sosongo.com", Nameservers: []string{"ns1.gateway.test"},
		Records: []ZoneRecord{{Name: "outside.test", Type: "MX", Value: "mail.test", Preference: ptrPreference(10)}}}}); err == nil {
		t.Fatal("accepted an out-of-zone record")
	}
	if again := exchangeHandler(server, "sosongo.com.", mdns.TypeMX); len(again.Answer) != 1 {
		t.Fatalf("invalid snapshot replaced live records: %+v", again)
	}
}

func ptrPreference(value uint16) *uint16 { return &value }

func TestHTTPZoneSourceAuthenticatesAndRefreshesServer(t *testing.T) {
	var receivedToken string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		receivedToken = request.Header.Get("X-Gateway-Internal")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"name":"api.example.test","nameservers":["ns1.example.test"],"ttl":30,"records":[{"name":"api.example.test","type":"MX","value":"route1.mx.cloudflare.net","preference":10}]}]}`)),
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
	mx := exchangeHandler(server, "api.example.test.", mdns.TypeMX)
	if mx.Rcode != mdns.RcodeSuccess || len(mx.Answer) != 1 || mx.Answer[0].(*mdns.MX).Mx != "route1.mx.cloudflare.net." {
		t.Fatalf("refreshed MX records were not served: %+v", mx)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
