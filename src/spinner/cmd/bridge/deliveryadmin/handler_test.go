package deliveryadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/webong/gateway/src/spinner/cmd/bridge"
	"github.com/webong/gateway/src/spinner/cmd/bridge/deliveryqueue"
	"github.com/webong/gateway/src/spinner/cmd/bridge/smtp"
)

const deliveryID = "0123456789abcdef0123456789abcdef"

type fakeQueue struct {
	status  deliveryqueue.Status
	retries int
}

type fakeRunHistory struct{ id string }

func (history fakeRunHistory) ReadRun(id string) ([]byte, error) {
	if id != history.id {
		return nil, os.ErrNotExist
	}
	return []byte(`{"id":"` + id + `","hook":"deploy"}`), nil
}

func TestHandlerServesShellHistoryOnlyWithPrivateToken(t *testing.T) {
	handler, err := NewHandler(nil, "private-token", WithShellRunHistory(fakeRunHistory{id: deliveryID}))
	if err != nil {
		t.Fatal(err)
	}
	path := "/shell/runs/" + deliveryID
	if response := serve(handler, http.MethodGet, path, "wrong-token"); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized shell history returned %d", response.Code)
	}
	response := serve(handler, http.MethodGet, path, "private-token")
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" || !bytes.Contains(response.Body.Bytes(), []byte(deliveryID)) {
		t.Fatalf("private shell history response: %d %s", response.Code, response.Body.String())
	}
	if response := serve(handler, http.MethodGet, "/shell/runs/missing", "private-token"); response.Code != http.StatusNotFound {
		t.Fatalf("missing shell history returned %d", response.Code)
	}
	if response := serve(handler, http.MethodPost, path, "private-token"); response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("shell history accepted mutation: %d", response.Code)
	}
	if response := serve(handler, http.MethodGet, "/deliveries", "private-token"); response.Code != http.StatusNotFound {
		t.Fatalf("shell-only private admin exposed delivery queue: %d", response.Code)
	}
}

type adapterQueue struct {
	*fakeQueue
	adapter string
}

func (queue *adapterQueue) SupportsAdapter(name string) bool { return name == queue.adapter }

type fakeEmailQueue struct {
	delivery bridge.GatewayDelivery
	accepted int
}

type unavailableSMTPQueue struct{ *fakeEmailQueue }

func (*unavailableSMTPQueue) SupportsAdapter(string) bool { return false }

type fakeSubmissionScanner struct{ infected bool }

func (scanner fakeSubmissionScanner) Scan(context.Context, []byte) (smtp.ScanVerdict, error) {
	return smtp.ScanVerdict{Infected: scanner.infected}, nil
}

type failingSubmissionSigner struct{}

func (failingSubmissionSigner) CheckSigningDomain(string) error { return errors.New("no key") }

func (queue *fakeEmailQueue) EnqueueGatewayIdempotent(delivery bridge.GatewayDelivery, id, fingerprint string, expiresAt time.Time) (bool, error) {
	if id != deliveryID || len(fingerprint) != 64 || !expiresAt.After(time.Now()) {
		return false, errors.New("invalid receipt")
	}
	queue.delivery = delivery
	queue.accepted++
	return false, nil
}

func (queue *fakeQueue) Status(id string) (deliveryqueue.Status, error) {
	if id != queue.status.ID {
		return deliveryqueue.Status{}, deliveryqueue.ErrNotFound
	}
	return queue.status, nil
}

func (queue *fakeQueue) List(state deliveryqueue.State, limit int) ([]deliveryqueue.Status, error) {
	if state != deliveryqueue.StateFailed {
		return nil, deliveryqueue.ErrInvalidState
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid limit")
	}
	return []deliveryqueue.Status{queue.status}, nil
}

func (queue *fakeQueue) ListEvent(eventID string, limit int) ([]deliveryqueue.Status, error) {
	if eventID == queue.status.EventID && limit > 0 && limit <= 100 {
		return []deliveryqueue.Status{queue.status}, nil
	}
	return []deliveryqueue.Status{}, nil
}

func (queue *fakeQueue) Retry(id string) error {
	if id != queue.status.ID {
		return deliveryqueue.ErrNotFound
	}
	queue.retries++
	queue.status.State = deliveryqueue.StateQueued
	return nil
}

func TestHandlerRequiresTokenBeforeInspectionOrRetry(t *testing.T) {
	queue := &fakeQueue{status: deliveryqueue.Status{ID: deliveryID, State: deliveryqueue.StateFailed}}
	handler, err := NewHandler(queue, "private-token")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/deliveries", "/deliveries/" + deliveryID, "/deliveries/" + deliveryID + "/retry"} {
		method := http.MethodGet
		if path == "/deliveries/"+deliveryID+"/retry" {
			method = http.MethodPost
		}
		response := serve(handler, method, path, "wrong-token")
		if response.Code != http.StatusUnauthorized || queue.retries != 0 {
			t.Fatalf("unauthorized access reached queue: %s %s => %d", method, path, response.Code)
		}
	}
}

func TestHandlerListsShowsAndRetriesFailedDelivery(t *testing.T) {
	queue := &fakeQueue{status: deliveryqueue.Status{ID: deliveryID, State: deliveryqueue.StateFailed, Adapter: "http", Attempts: 3}}
	handler, _ := NewHandler(queue, "private-token")
	listed := serve(handler, http.MethodGet, "/deliveries?state=failed&limit=10", "private-token")
	if listed.Code != http.StatusOK || listed.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected list response: %d %s", listed.Code, listed.Body.String())
	}
	var result struct {
		Data []deliveryqueue.Status `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &result); err != nil || len(result.Data) != 1 || result.Data[0].ID != deliveryID {
		t.Fatalf("unexpected list result: %+v, %v", result, err)
	}
	shown := serve(handler, http.MethodGet, "/deliveries/"+deliveryID, "private-token")
	if shown.Code != http.StatusOK {
		t.Fatalf("unexpected show response: %d", shown.Code)
	}
	retried := serve(handler, http.MethodPost, "/deliveries/"+deliveryID+"/retry", "private-token")
	if retried.Code != http.StatusAccepted || queue.retries != 1 {
		t.Fatalf("unexpected retry response: %d %s", retried.Code, retried.Body.String())
	}
}

func TestMultiQueueListsAndRetriesAcrossSeparateSpools(t *testing.T) {
	first := &fakeQueue{status: deliveryqueue.Status{ID: deliveryID, State: deliveryqueue.StateFailed, UpdatedAt: time.Now().Add(-time.Minute)}}
	secondID := "fedcba9876543210fedcba9876543210"
	second := &fakeQueue{status: deliveryqueue.Status{ID: secondID, State: deliveryqueue.StateFailed, UpdatedAt: time.Now()}}
	multi := NewMultiQueue(first, second)
	statuses, err := multi.List(deliveryqueue.StateFailed, 1)
	if err != nil || len(statuses) != 1 || statuses[0].ID != secondID {
		t.Fatalf("unexpected merged status list: %+v, %v", statuses, err)
	}
	if err := multi.Retry(secondID); err != nil || first.retries != 0 || second.retries != 1 {
		t.Fatalf("retry targeted the wrong spool: first=%d second=%d err=%v", first.retries, second.retries, err)
	}
	if status, err := multi.Status(secondID); err != nil || status.State != deliveryqueue.StateQueued {
		t.Fatalf("status did not resolve the second spool: %+v, %v", status, err)
	}
}

func TestMultiQueueDeduplicatesSharedStoreAndRetriesWithOwningAdapter(t *testing.T) {
	status := deliveryqueue.Status{ID: deliveryID, EventID: "event-123", Adapter: "smtp", State: deliveryqueue.StateFailed, UpdatedAt: time.Now()}
	httpQueue := &adapterQueue{fakeQueue: &fakeQueue{status: status}, adapter: "http"}
	smtpQueue := &adapterQueue{fakeQueue: &fakeQueue{status: status}, adapter: "smtp"}
	multi := NewMultiQueue(httpQueue, smtpQueue)
	statuses, err := multi.List(deliveryqueue.StateFailed, 10)
	if err != nil || len(statuses) != 1 || statuses[0].ID != deliveryID {
		t.Fatalf("shared delivery was duplicated: %+v, %v", statuses, err)
	}
	statuses, err = multi.ListEvent(status.EventID, 10)
	if err != nil || len(statuses) != 1 || statuses[0].ID != deliveryID {
		t.Fatalf("shared event delivery was duplicated: %+v, %v", statuses, err)
	}
	if err := multi.Retry(deliveryID); err != nil || httpQueue.retries != 0 || smtpQueue.retries != 1 {
		t.Fatalf("retry did not select SMTP worker: http=%d smtp=%d err=%v", httpQueue.retries, smtpQueue.retries, err)
	}
}

func TestHandlerFindsEventDeliveriesAcrossSpools(t *testing.T) {
	eventID := "event-123"
	first := &fakeQueue{status: deliveryqueue.Status{ID: deliveryID, EventID: eventID, State: deliveryqueue.StateFailed, UpdatedAt: time.Now().Add(-time.Minute)}}
	secondID := "fedcba9876543210fedcba9876543210"
	second := &fakeQueue{status: deliveryqueue.Status{ID: secondID, EventID: eventID, State: deliveryqueue.StateDelivered, UpdatedAt: time.Now()}}
	handler, err := NewHandler(NewMultiQueue(first, second), "private-token")
	if err != nil {
		t.Fatal(err)
	}
	response := serve(handler, http.MethodGet, "/deliveries?event_id=event-123&limit=1", "private-token")
	var result struct {
		Data []deliveryqueue.Status `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || response.Code != http.StatusOK || len(result.Data) != 1 || result.Data[0].ID != secondID {
		t.Fatalf("unexpected filtered statuses: %d %+v %v", response.Code, result, err)
	}
}

func TestHandlerRejectsInvalidRequestsWithoutLeakingErrors(t *testing.T) {
	queue := &fakeQueue{status: deliveryqueue.Status{ID: deliveryID, State: deliveryqueue.StateFailed}}
	handler, _ := NewHandler(queue, "private-token")
	for _, test := range []struct {
		method, path string
		expected     int
	}{
		{http.MethodGet, "/deliveries?limit=101", http.StatusBadRequest},
		{http.MethodGet, "/deliveries?state=unknown", http.StatusBadRequest},
		{http.MethodGet, "/deliveries?event_id=", http.StatusBadRequest},
		{http.MethodGet, "/deliveries?event_id=bad%2Fid", http.StatusBadRequest},
		{http.MethodGet, "/deliveries?event_id=event-123&state=failed", http.StatusBadRequest},
		{http.MethodGet, "/deliveries/missing", http.StatusNotFound},
		{http.MethodPost, "/deliveries", http.StatusMethodNotAllowed},
		{http.MethodGet, "/deliveries/" + deliveryID + "/retry", http.StatusMethodNotAllowed},
	} {
		response := serve(handler, test.method, test.path, "private-token")
		if response.Code != test.expected {
			t.Fatalf("%s %s returned %d, expected %d", test.method, test.path, response.Code, test.expected)
		}
	}
}

func TestPrivateEmailSubmissionRequiresTokenAndAuthorizedAuthor(t *testing.T) {
	spool := &fakeEmailQueue{}
	handler, err := NewHandler(&fakeQueue{}, "private-token", WithEmailSubmission(spool, nil, nil, 1024, []byte("private-loop-key"), false))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"receipt_id": deliveryID,
		"mail_from":  "alice@example.test", "authorized_senders": []string{"alice@example.test"},
		"recipients": []string{"bob@example.test"}, "message": []byte("From: alice@example.test\r\n\r\nhello"),
	}
	encoded, _ := json.Marshal(input)
	submit := func(token string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := submit("wrong-token", encoded); response.Code != http.StatusUnauthorized || spool.accepted != 0 {
		t.Fatalf("unauthorized submission reached spool: %d", response.Code)
	}
	if response := submit("private-token", encoded); response.Code != http.StatusAccepted || spool.accepted != 1 || spool.delivery.Protocol != bridge.ProtocolSMTP || spool.delivery.Attributes[bridge.DKIMSigningDomainAttribute] != "example.test" || spool.delivery.Attributes[bridge.FailureNoticeToAttribute] != "" {
		t.Fatalf("valid submission was not queued: %d %s", response.Code, response.Body.String())
	}
	input["mail_from"] = "spoof@example.test"
	encoded, _ = json.Marshal(input)
	if response := submit("private-token", encoded); response.Code != http.StatusUnprocessableEntity || spool.accepted != 1 {
		t.Fatalf("spoofed sender reached spool: %d", response.Code)
	}
	input["mail_from"] = "alice@example.test"
	input["message"] = []byte("From: spoof@example.test\r\n\r\nhello")
	encoded, _ = json.Marshal(input)
	if response := submit("private-token", encoded); response.Code != http.StatusUnprocessableEntity || spool.accepted != 1 {
		t.Fatalf("spoofed author reached spool: %d", response.Code)
	}
}

func TestPrivateEmailSubmissionRequiresGatewayOutboundAdapter(t *testing.T) {
	spool := &unavailableSMTPQueue{fakeEmailQueue: &fakeEmailQueue{}}
	handler, err := NewHandler(&fakeQueue{}, "private-token", WithEmailSubmission(spool, nil, nil, 1024, []byte("private-loop-key"), false))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"receipt_id": deliveryID, "mail_from": "alice@example.test",
		"authorized_senders": []string{"alice@example.test"},
		"recipients":         []string{"bob@example.test"},
		"message":            []byte("From: alice@example.test\r\n\r\nhello"),
	})
	request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || spool.accepted != 0 {
		t.Fatalf("submission without an outbound adapter was accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestPrivateEmailSubmissionFailsClosedForSigningAndMalware(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"receipt_id": deliveryID,
		"mail_from":  "alice@example.test", "authorized_senders": []string{"alice@example.test"},
		"recipients": []string{"bob@example.test"}, "message": []byte("From: alice@example.test\r\n\r\nhello"),
	})
	for _, test := range []struct {
		name    string
		checker signingChecker
		scanner smtp.MalwareScanner
		status  int
	}{
		{"missing key", failingSubmissionSigner{}, nil, http.StatusServiceUnavailable},
		{"infected", nil, fakeSubmissionScanner{infected: true}, http.StatusUnprocessableEntity},
	} {
		t.Run(test.name, func(t *testing.T) {
			spool := &fakeEmailQueue{}
			handler, err := NewHandler(&fakeQueue{}, "private-token", WithEmailSubmission(spool, test.checker, test.scanner, 1024, []byte("loop-key"), false))
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
			request.Header.Set("Authorization", "Bearer private-token")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status || spool.accepted != 0 {
				t.Fatalf("unsafe message reached spool: status=%d accepted=%d", response.Code, spool.accepted)
			}
		})
	}
}

func TestPrivateEmailSubmissionFailureNoticeUsesAuthenticatedSender(t *testing.T) {
	notices := make(chan bridge.GatewayDelivery, 2)
	spool, err := deliveryqueue.New(deliveryqueue.Config{
		Path: t.TempDir(), MaxAttempts: 1, PollInterval: 10 * time.Millisecond,
		FailureNoticeDomain: "gateway.example.test",
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"smtp": bridge.GatewayDeliveryAdapterFunc(func(_ context.Context, delivery bridge.GatewayDelivery) error {
				if delivery.Attributes["mail_from"] == "" {
					notices <- delivery
					return nil
				}
				return errors.New("test relay rejected the message")
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := spool.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	handler, err := NewHandler(spool, "private-token", WithEmailSubmission(spool, nil, nil, 1024, []byte("loop-key"), true))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"receipt_id": deliveryID, "mail_from": "alice@example.test",
		"authorized_senders": []string{"alice@example.test"},
		"recipients":         []string{"bob@example.test"},
		"message":            []byte("From: alice@example.test\r\nSubject: failed\r\n\r\nhello"),
	})
	request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("valid submission was not queued: %d %s", response.Code, response.Body.String())
	}
	select {
	case notice := <-notices:
		if notice.Target != "alice@example.test" || notice.Attributes["mail_from"] != "" ||
			!bytes.Contains(notice.Payload, []byte("Delivery ID: "+deliveryID)) {
			t.Fatalf("failure notice was not safely addressed to the authenticated sender: %+v", notice)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("failed HTTP submission did not generate a failure notice")
	}
}

func TestPrivateEmailSubmissionReplaysOneDurableReceipt(t *testing.T) {
	var deliveries atomic.Int32
	spool, err := deliveryqueue.New(deliveryqueue.Config{
		Path: t.TempDir(),
		Adapters: map[string]bridge.GatewayDeliveryAdapter{
			"smtp": bridge.GatewayDeliveryAdapterFunc(func(context.Context, bridge.GatewayDelivery) error {
				deliveries.Add(1)
				return nil
			}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := spool.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	handler, err := NewHandler(spool, "private-token", WithEmailSubmission(spool, nil, nil, 1024, []byte("loop-key"), false))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{
		"receipt_id": deliveryID, "mail_from": "alice@example.test",
		"authorized_senders": []string{"alice@example.test"},
		"recipients":         []string{"bob@example.test"},
		"message":            []byte("From: alice@example.test\r\n\r\nhello"),
	}
	submit := func() *httptest.ResponseRecorder {
		body, _ := json.Marshal(input)
		request := httptest.NewRequest(http.MethodPost, "/email/submissions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer private-token")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := submit()
	second := submit()
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted || !bytes.Contains(second.Body.Bytes(), []byte(`"replayed":true`)) {
		t.Fatalf("idempotent submission did not replay: first=%d second=%d %s", first.Code, second.Code, second.Body.String())
	}
	input["message"] = []byte("From: alice@example.test\r\n\r\nchanged")
	if response := submit(); response.Code != http.StatusConflict {
		t.Fatalf("changed message returned %d", response.Code)
	}
	for deadline := time.Now().Add(time.Second); deliveries.Load() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	if deliveries.Load() != 1 {
		t.Fatalf("expected one relay attempt, got %d", deliveries.Load())
	}
}

func serve(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
