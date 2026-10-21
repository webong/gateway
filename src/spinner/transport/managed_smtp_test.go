package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/smtpout"
)

func TestManagedSMTPFailoverIsConservative(t *testing.T) {
	tests := []struct {
		name            string
		first           error
		want            []string
		uncertain       bool
		capacityPrimary bool
	}{
		{"primary success", nil, []string{"primary"}, false, false},
		{"preaccept failover", smtpout.ErrPreAcceptConnection, []string{"primary", "backup"}, false, false},
		{"temporary recipient failover", &smtpclient.SMTPError{Code: 451}, []string{"primary", "backup"}, false, false},
		{"permanent recipient stops", bridge.ErrPermanentDelivery, []string{"primary"}, false, false},
		{"ambiguous data stops", bridge.ErrUncertainDelivery, []string{"primary"}, true, false},
		{"primary capacity uses backup", nil, []string{"backup"}, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			var states []string
			authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Gateway-Internal") != "secret" {
					http.NotFound(w, r)
					return
				}
				if r.URL.Path == "/_internal/gateway/email/outbound-plan" {
					_ = json.NewEncoder(w).Encode(map[string]any{"transports": []outboundSMTPTransport{
						{ID: "primary", Type: "smtp", Address: "smtp.primary.test:587", TLSMode: "starttls", Username: "u", Password: "p"},
						{ID: "backup", Type: "smtp", Address: "smtp.backup.test:587", TLSMode: "starttls", Username: "u", Password: "p"},
					}})
					return
				}
				if strings.HasSuffix(r.URL.Path, "/reserve") {
					var reservation struct {
						TransportID string `json:"transport_id"`
					}
					_ = json.NewDecoder(r.Body).Decode(&reservation)
					if test.capacityPrimary && reservation.TransportID == "primary" {
						w.WriteHeader(http.StatusTooManyRequests)
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]bool{"reserved": true})
					return
				}
				var event struct {
					State string `json:"state"`
				}
				_ = json.NewDecoder(r.Body).Decode(&event)
				mu.Lock()
				states = append(states, event.State)
				mu.Unlock()
				_ = json.NewEncoder(w).Encode(map[string]string{"state": event.State})
			}))
			defer authority.Close()
			adapter, err := newManagedSMTPAdapter(authority.URL, "secret", time.Second, nil)
			if err != nil {
				t.Fatal(err)
			}
			adapter.send = func(_ context.Context, spec outboundSMTPTransport, delivery bridge.GatewayDelivery) error {
				mu.Lock()
				seen = append(seen, spec.ID)
				mu.Unlock()
				if delivery.Attributes["mail_from"] != "support@sender.test" {
					t.Fatal("visible From changed")
				}
				if spec.ID == "primary" {
					return test.first
				}
				return nil
			}
			delivery := bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP, Adapter: "managed.smtp", Target: "buyer@example.test",
				Payload: []byte("From: support@sender.test\r\nReply-To: conversation@reply.sender.test\r\n\r\nHello"),
				Attributes: map[string]string{"gateway_delivery_id": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "outbound_pool_id": "pool",
					"account_id": "one", "outbound_route_id": "route", "outbound_domain_id": "", "outbound_sender_domain": "sender.test",
					"mail_from": "support@sender.test"}}
			err = adapter.DeliverGateway(context.Background(), delivery)
			if !reflect.DeepEqual(seen, test.want) {
				t.Fatalf("attempt order %v, want %v", seen, test.want)
			}
			if test.uncertain && !errors.Is(err, bridge.ErrUncertainDelivery) {
				t.Fatalf("uncertain outcome lost: %v", err)
			}
			if !test.uncertain && test.first != nil && len(test.want) == 1 && err == nil {
				t.Fatal("failure was accepted")
			}
			if len(states) == 0 || states[0] != "attempted" {
				t.Fatalf("missing durable attempt record: %v", states)
			}
		})
	}
}
