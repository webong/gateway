package smtp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"

	bridge "github.com/webong/gateway/arranger/protocol"
)

func deliverCallback(ctx context.Context, delivery bridge.GatewayDelivery, client *http.Client) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.Target, bytes.NewReader(delivery.Payload))
	if err != nil {
		return err
	}
	request.Header = http.Header(delivery.Headers).Clone()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("callback rejected")
	}
	return nil
}

func TestSMTPToSignedHTTPSCallbackRoundTrip(t *testing.T) {
	const routeID = "11111111-1111-4111-8111-111111111111"
	const recipient = "support@acme.test"
	const secret = "client-generated-secret"
	verified := make(chan []byte, 1)
	callback := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read callback: %v", err)
			writer.WriteHeader(500)
			return
		}
		eventID := request.Header.Get("X-Gateway-Event-Id")
		envelope := request.Header.Get("X-Gateway-Envelope")
		digest := sha256.Sum256(body)
		input := routeID + "\n" + eventID + "\n" + envelope + "\n" + hex.EncodeToString(digest[:])
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(input))
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if request.Header.Get("X-Gateway-Route-Id") != routeID || request.Header.Get("X-Gateway-Signature") != want ||
			request.Header.Get("Content-Type") != "message/rfc822" {
			t.Errorf("callback signature or metadata mismatch: %+v", request.Header)
			writer.WriteHeader(401)
			return
		}
		var decoded struct {
			MailFrom   string   `json:"mail_from"`
			Recipients []string `json:"recipients"`
		}
		encoded, err := base64.StdEncoding.DecodeString(envelope)
		if err != nil || json.Unmarshal(encoded, &decoded) != nil || decoded.MailFrom != "sender@outside.test" ||
			len(decoded.Recipients) != 1 || decoded.Recipients[0] != recipient {
			t.Errorf("callback envelope mismatch: %s", envelope)
			writer.WriteHeader(400)
			return
		}
		verified <- body
		writer.WriteHeader(202)
	}))
	defer callback.Close()

	executor := &recordingExecutor{}
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		if event.Kind == bridge.EventRecipient {
			if event.Route != recipient {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayReject, StatusCode: 550}, nil
			}
			return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP, Action: bridge.GatewayAccept, StatusCode: 250}, nil
		}
		envelope, _ := json.Marshal(map[string]any{"mail_from": event.Attributes["mail_from"], "recipients": []string{recipient}})
		encoded := base64.StdEncoding.EncodeToString(envelope)
		digest := sha256.Sum256(event.Payload)
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(routeID + "\n" + event.ID + "\n" + encoded + "\n" + hex.EncodeToString(digest[:])))
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolSMTP,
			Action: bridge.GatewayDeliver, StatusCode: 250,
			Deliveries: []bridge.GatewayDelivery{{Protocol: bridge.ProtocolHTTP, Target: callback.URL,
				Payload: event.Payload, Headers: map[string][]string{
					"Content-Type": {"message/rfc822"}, "X-Gateway-Route-Id": {routeID},
					"X-Gateway-Event-Id": {event.ID}, "X-Gateway-Envelope": {encoded},
					"X-Gateway-Signature": {"sha256=" + hex.EncodeToString(mac.Sum(nil))},
				}}}}, nil
	})
	server, err := NewServer(Config{Address: "127.0.0.1:0", Hostname: "mx.gateway.test",
		PlannerTimeout: time.Second, RecipientPreflight: true}, planner, executor)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ErrServerClosed) {
			t.Errorf("shutdown: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not stop")
		}
	})

	client, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Hello("client.test"); err != nil {
		t.Fatal(err)
	}
	if err := client.Mail("sender@outside.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt(recipient, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt("unknown@acme.test", nil); err == nil {
		t.Fatal("unknown recipient was accepted")
	}
	data, err := client.Data()
	if err != nil {
		t.Fatal(err)
	}
	mime := "From: sender@outside.test\r\nTo: support@acme.test\r\nSubject: test\r\n\r\nhello\r\n"
	if _, err := io.WriteString(data, mime); err != nil {
		t.Fatal(err)
	}
	if err := data.Close(); err != nil {
		t.Fatal(err)
	}
	if got := len(executor.snapshot()); got != 1 {
		t.Fatalf("expected one durable queued callback, got %d", got)
	}
	if err := deliverCallback(context.Background(), executor.snapshot()[0], callback.Client()); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-verified:
		if string(body) != mime {
			t.Fatalf("MIME changed in transit: %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("signed HTTPS callback was not received")
	}
}
