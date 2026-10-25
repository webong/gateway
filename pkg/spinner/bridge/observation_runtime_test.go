package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/integration"
)

func TestObservationSinksUsePrivateLanguageNeutralContract(t *testing.T) {
	assertRequest := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ObservationPath || r.Header.Get("X-Gateway-Internal") != "secret" {
			t.Fatalf("unexpected observation request: path=%s headers=%v", r.URL.Path, r.Header)
		}
		var observation integration.Observation
		if err := json.NewDecoder(r.Body).Decode(&observation); err != nil {
			t.Fatal(err)
		}
		if observation.Type != "dns.query" || observation.Attributes["mode"] != "managed" {
			t.Fatalf("unexpected observation: %+v", observation)
		}
		w.WriteHeader(http.StatusAccepted)
	}
	observation := integration.Observation{
		Version: integration.ObservationVersion, ID: "dns-1", Type: "dns.query",
		OccurredAt: time.Now().UTC(), Attributes: map[string]string{"mode": "managed"},
	}

	t.Run("embedded PHP", func(t *testing.T) {
		sink := NewPHPObservationSink(http.HandlerFunc(assertRequest), "secret")
		if err := sink.RecordObservation(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("remote HTTP", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(assertRequest))
		defer server.Close()
		sink, err := NewHTTPObservationSink(server.URL, server.Client(), "secret")
		if err != nil {
			t.Fatal(err)
		}
		if err := sink.RecordObservation(context.Background(), observation); err != nil {
			t.Fatal(err)
		}
	})
}
