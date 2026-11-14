package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	plugin "github.com/webong/ext/pkg/plugin-go/contract"
	arranger "github.com/webong/gateway/arranger/integration"
)

type reverseFunc func(context.Context, plugin.ContractRef, string, json.RawMessage) (json.RawMessage, error)

func (f reverseFunc) Call(ctx context.Context, ref plugin.ContractRef, op string, raw json.RawMessage) (json.RawMessage, error) {
	return f(ctx, ref, op, raw)
}

func TestWorkReceiptSurvivesOutboxReservationAndProcessReplacement(t *testing.T) {
	event := workEvent{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: "work.ready", Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	event.Payload.ClaimID = "cccccccccccccccccccccccccccccccc"
	first, err := workEnqueue(event, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !opaqueID.MatchString(first.ID) {
		t.Fatal("receipt must use Gateway queue 128-bit ID format")
	}
	// Gateway accepted the notification but the process died before its outbox
	// acknowledgement. A replacement reserves a fresh token for the same event.
	event.Token = "dddddddddddddddddddddddddddddddd"
	next, err := workEnqueue(event, time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != next.ID || first.Fingerprint != next.Fingerprint {
		t.Fatal("retry changed durable receipt identity")
	}
	event.Payload.ClaimID = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	changed, _ := workEnqueue(event, time.Unix(200, 0))
	if first.ID != changed.ID || first.Fingerprint == changed.Fingerprint {
		t.Fatal("changed claim did not conflict with original receipt")
	}
}
func TestReceiptConflictAndCapabilityLossFailClosed(t *testing.T) {
	event := workEvent{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: "work.ready", Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	event.Payload.ClaimID = "cccccccccccccccccccccccccccccccc"
	caller := reverseFunc(func(_ context.Context, ref plugin.ContractRef, op string, raw json.RawMessage) (json.RawMessage, error) {
		if ref != arranger.DeliveryContract {
			t.Fatal("wrong reverse contract")
		}
		if op == arranger.OperationEnqueueGatewayIdempotent {
			return json.RawMessage(`{"conflict":true}`), nil
		}
		return json.RawMessage(`{"enabled":false}`), nil
	})
	if err := enqueueWork(context.Background(), caller, event); !errors.Is(err, arranger.ErrIdempotencyConflict) {
		t.Fatalf("conflict accepted: %v", err)
	}
	if durableReady(context.Background(), caller) == nil {
		t.Fatal("missing reserved-receipt capability accepted")
	}
	s := New(t.TempDir())
	s.publishLoop(caller)
	select {
	case <-s.stopped:
	default:
		t.Fatal("runtime survived lost durable capability")
	}
}

func TestPublisherRecoversEnqueueAndAcknowledgementFailures(t *testing.T) {
	s := New(t.TempDir())
	s.contextFile = filepath.Join(t.TempDir(), "context.json")
	event := workEvent{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Type: "work.ready", Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	event.Payload.ClaimID = "cccccccccccccccccccccccccccccccc"
	var mu sync.Mutex
	enqueues, acks, retries := 0, 0, 0
	var receipt arranger.IdempotentEnqueueRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var request struct {
			Method string `json:"method"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			t.Error("invalid private request")
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("Authorization") != "Bearer private-control" {
			t.Error("missing private credential")
		}
		switch request.Method {
		case "work.reserve":
			_ = json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"events": []workEvent{event}}})
		case "work.retry":
			retries++
			event.Token = "dddddddddddddddddddddddddddddddd"
			_, _ = w.Write([]byte(`{"result":[]}`))
		case "work.ack":
			acks++
			if acks == 1 {
				event.Token = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
				w.WriteHeader(503)
				return
			}
			_, _ = w.Write([]byte(`{"result":[]}`))
			s.close()
		default:
			t.Error("unexpected private method")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	s.backend = strings.TrimPrefix(server.URL, "http://")
	s.controlToken = "private-control"
	caller := reverseFunc(func(_ context.Context, _ plugin.ContractRef, op string, raw json.RawMessage) (json.RawMessage, error) {
		if op != arranger.OperationEnqueueGatewayIdempotent {
			return json.RawMessage(`{"enabled":true}`), nil
		}
		mu.Lock()
		defer mu.Unlock()
		enqueues++
		var request arranger.IdempotentEnqueueRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if enqueues == 1 {
			receipt = request
			return nil, errors.New("temporary queue failure")
		}
		if request.ID != receipt.ID || request.Fingerprint != receipt.Fingerprint {
			t.Error("retry changed receipt identity")
		}
		return json.RawMessage(`{"replayed":true}`), nil
	})
	done := make(chan struct{})
	go func() { s.publishLoop(caller); close(done) }()
	defer s.close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher did not recover")
	}
	mu.Lock()
	defer mu.Unlock()
	if enqueues != 3 || acks != 2 || retries != 1 {
		t.Fatalf("handoff recovery calls: enqueues=%d acks=%d retries=%d", enqueues, acks, retries)
	}
}
