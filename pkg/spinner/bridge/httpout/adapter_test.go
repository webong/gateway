package httpout

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	bridge "github.com/webong/gateway/pkg/spinner/bridge"
	"github.com/webong/gateway/pkg/spinner/internal/forwarding"
)

type fakeForwarder struct {
	request forwarding.ForwardRequest
	status  int
	body    []byte
	err     error
}

func (fake *fakeForwarder) ForwardStatus(request forwarding.ForwardRequest) (int, error) {
	fake.request = request
	return fake.status, fake.err
}

func (fake *fakeForwarder) ForwardSync(request forwarding.ForwardRequest) (forwarding.ForwardResponse, error) {
	fake.request = request
	return forwarding.ForwardResponse{StatusCode: fake.status, Body: fake.body}, fake.err
}

func TestAdapterPreservesResolvedHTTPRequest(t *testing.T) {
	fake := &fakeForwarder{status: http.StatusAccepted}
	adapter, err := NewAdapter(fake)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	delivery := bridge.GatewayDelivery{
		Protocol: bridge.ProtocolHTTP, Target: "https://example.test/hook?token=abc",
		Attributes: map[string]string{"method": http.MethodPatch, "raw_query": "source=gateway", "private_callback_cidrs": "172.18.0.0/16"},
		Headers:    map[string][]string{"X-Test": {"before"}}, Payload: []byte("payload"),
		Transport: &bridge.HTTPTransport{
			Proxy:                 &bridge.HTTPProxy{HTTPS: "https://proxy.example.test", NoProxy: []string{"internal.example.test"}},
			ConnectTimeoutSeconds: 2.5, RequestTimeoutSeconds: 10,
		},
	}
	if err := adapter.DeliverGateway(ctx, delivery); err != nil {
		t.Fatal(err)
	}
	request := fake.request
	if request.Context != ctx || request.TargetURL != delivery.Target || request.Method != http.MethodPatch || request.RawQuery != "source=gateway" || string(request.RequestBody) != "payload" {
		t.Fatalf("HTTP request lost delivery fields: %+v", request)
	}
	if http.Header(request.Headers).Get("X-Test") != "before" || request.Transport.HTTPSProxy != "https://proxy.example.test" || request.Transport.ConnectTimeout != 2500*time.Millisecond || request.Transport.RequestTimeout != 10*time.Second || request.Transport.PrivateCallbackCIDRs != "172.18.0.0/16" {
		t.Fatalf("HTTP request lost transport fields: %+v", request)
	}
	delivery.Headers["X-Test"][0] = "after"
	if http.Header(request.Headers).Get("X-Test") != "before" {
		t.Fatal("queued header values were not cloned")
	}
}

func TestAdapterRetriesNonSuccessAndTransportErrors(t *testing.T) {
	fake := &fakeForwarder{status: http.StatusServiceUnavailable}
	adapter, _ := NewAdapter(fake)
	delivery := bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://example.test/hook"}
	if err := adapter.DeliverGateway(context.Background(), delivery); err == nil {
		t.Fatal("non-2xx HTTP response must fail the delivery attempt")
	}
	fake.status = http.StatusBadRequest
	if err := adapter.DeliverGateway(context.Background(), delivery); !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("400 should fail permanently, got %v", err)
	}
	fake.err = errors.New("connection failed")
	if err := adapter.DeliverGateway(context.Background(), delivery); !errors.Is(err, fake.err) {
		t.Fatalf("transport error was not propagated: %v", err)
	}
}

func TestAdapterAssertsExpectedStatus(t *testing.T) {
	fake := &fakeForwarder{status: http.StatusOK}
	adapter, _ := NewAdapter(fake)
	delivery := bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://example.test/check", Attributes: map[string]string{"expected_status": "204"}}
	result, err := adapter.DeliverGatewayResult(context.Background(), delivery)
	if result.ResponseStatus != http.StatusOK || !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("mismatch should be terminal and retain observed status: result=%+v err=%v", result, err)
	}
	fake.status = http.StatusNoContent
	result, err = adapter.DeliverGatewayResult(context.Background(), delivery)
	if err != nil || result.ResponseStatus != http.StatusNoContent {
		t.Fatalf("matching status should pass: result=%+v err=%v", result, err)
	}
	fake.status = http.StatusNotFound
	delivery.Attributes["expected_status"] = "404"
	if _, err = adapter.DeliverGatewayResult(context.Background(), delivery); err != nil {
		t.Fatalf("explicit 404 assertion should pass: %v", err)
	}
	delivery.Attributes["expected_status"] = "bogus"
	if _, err = adapter.DeliverGatewayResult(context.Background(), delivery); !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("malformed assertion should fail permanently: %v", err)
	}
}

func TestAdapterAssertsResponseBodyDigestWithoutExposingBody(t *testing.T) {
	fake := &fakeForwarder{status: http.StatusOK, body: []byte("healthy")}
	adapter, _ := NewAdapter(fake)
	delivery := bridge.GatewayDelivery{
		Protocol: bridge.ProtocolHTTP, Target: "https://example.test/check",
		Attributes: map[string]string{"expected_body_sha256": "87695fdac81728b9d7f2d4a1335c2632bb5e6ba1bed21d2dff0254fba31c7d5b"},
	}
	result, err := adapter.DeliverGatewayResult(context.Background(), delivery)
	if err != nil || result.BodyAssertion != "matched" || result.ResponseStatus != http.StatusOK {
		t.Fatalf("matching body digest should pass: result=%+v err=%v", result, err)
	}
	fake.body = []byte("unhealthy")
	result, err = adapter.DeliverGatewayResult(context.Background(), delivery)
	if !errors.Is(err, bridge.ErrPermanentDelivery) || result.BodyAssertion != "mismatched" || result.ResponseStatus != http.StatusOK {
		t.Fatalf("mismatched body digest should fail permanently: result=%+v err=%v", result, err)
	}
	delivery.Attributes["expected_body_sha256"] = "not-a-digest"
	if _, err = adapter.DeliverGatewayResult(context.Background(), delivery); !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("invalid body digest should fail before forwarding: %v", err)
	}
	fake.err = forwarding.ErrResponseTooLarge
	delivery.Attributes["expected_body_sha256"] = "87695fdac81728b9d7f2d4a1335c2632bb5e6ba1bed21d2dff0254fba31c7d5b"
	if _, err = adapter.DeliverGatewayResult(context.Background(), delivery); !errors.Is(err, bridge.ErrPermanentDelivery) {
		t.Fatalf("oversized body should fail permanently: %v", err)
	}
}
