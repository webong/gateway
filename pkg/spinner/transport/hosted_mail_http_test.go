package transport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	emaildelivery "github.com/webong/gateway/pkg/email/spinner/delivery"
	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

func TestHostedMailAdaptersIsolateTwoAccountsAndRetryWithOneIdempotencyKey(t *testing.T) {
	type received struct{ key, account, body string }
	var mu sync.Mutex
	calls := map[string][]received{}
	servers := map[string]*httptest.Server{}
	for _, account := range []string{"account-a", "account-b"} {
		account := account
		servers[account] = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Header.Get("Authorization") != "Bearer token-"+account || request.Header.Get("Content-Type") != "message/rfc822" {
				t.Error("provider authentication or content type missing")
			}
			envelopeJSON, err := base64.StdEncoding.DecodeString(request.Header.Get("X-Gateway-Envelope"))
			if err != nil {
				t.Error(err)
			}
			var envelope map[string]string
			if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
				t.Error(err)
			}
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			calls[account] = append(calls[account], received{request.Header.Get("Idempotency-Key"), envelope["account_id"], string(body)})
			count := len(calls[account])
			mu.Unlock()
			if account == "account-a" && count == 1 {
				writer.WriteHeader(503)
				return
			}
			writer.WriteHeader(204)
		}))
		defer servers[account].Close()
		name := "zorvia." + account
		if err := RegisterDeliveryAdapterFactory(name, func(*config.Config) (bridge.GatewayDeliveryAdapter, error) {
			return emaildelivery.NewHostedMailHTTPAdapter(emaildelivery.HostedMailAdapterSpec{URL: servers[account].URL, BearerToken: "token-" + account}, servers[account].Client())
		}); err != nil {
			t.Fatal(err)
		}
	}
	settings := &config.Config{DeliverySpoolPath: t.TempDir(), DeliveryMaxAttempts: 3,
		DeliveryInitialBackoff: 10 * time.Millisecond, DeliveryMaxBackoff: 30 * time.Millisecond,
		DeliveryPollInterval: 10 * time.Millisecond, RequestTimeout: time.Second}
	queue, err := NewDeliveryQueue(settings, forwarding.NewForwarder(settings, logging.NewLogger("error")), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = queue.Close(ctx)
	}()
	for _, account := range []string{"account-a", "account-b"} {
		if err := queue.EnqueueGateway(bridge.GatewayDelivery{Protocol: bridge.ProtocolSMTP,
			Adapter: "zorvia." + account, Target: "mailbox-" + account, SubscriberID: account,
			Attributes: map[string]string{"event_id": "event-" + account, "account_id": account,
				"recipient": "support@" + account + ".test", "mail_from": "sender@outside.test"},
			Payload: []byte("Subject: " + account + "\r\n\r\nbody\r\n"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		statuses, err := queue.List(deliveryqueue.StateDelivered, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(statuses) == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls["account-a"]) != 2 || len(calls["account-b"]) != 1 {
		t.Fatalf("unexpected provider attempts: %+v", calls)
	}
	if calls["account-a"][0].key == "" || calls["account-a"][0].key != calls["account-a"][1].key {
		t.Fatal("retry changed its idempotency key")
	}
	for _, account := range []string{"account-a", "account-b"} {
		for _, received := range calls[account] {
			if received.account != account || received.body != "Subject: "+account+"\r\n\r\nbody\r\n" {
				t.Fatalf("mail crossed accounts: %+v", calls)
			}
		}
	}
}
