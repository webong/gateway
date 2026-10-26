package deliveryadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/webong/gateway/arranger/automation"
	"github.com/webong/gateway/pkg/spinner/bridge/deliveryqueue"
)

const deliveryID = "0123456789abcdef0123456789abcdef"

type fakeQueue struct {
	status  deliveryqueue.Status
	retries int
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

type fakeRunHistory struct{ id string }

func (history fakeRunHistory) ReadRun(id string) ([]byte, error) {
	if id != history.id {
		return nil, os.ErrNotExist
	}
	return []byte(`{"id":"` + id + `","hook":"deploy"}`), nil
}

type fakeAutomationExecutor struct{ outcome automation.Outcome }

func (executor fakeAutomationExecutor) Execute(_ context.Context, script automation.Script, input automation.Input) (automation.Outcome, error) {
	if script.Language != "javascript" || script.Source != "source" || input.Event["protocol"] != "imap" {
		return automation.Outcome{}, errors.New("unexpected input")
	}
	return executor.outcome, nil
}

func (fakeAutomationExecutor) Supports(language automation.Language) bool {
	return language == "javascript"
}

func TestHandlerExecutesOneCredentialRequestOnPrivateListener(t *testing.T) {
	handler, err := NewHandler(nil, "private-token", WithAutomationExecutor(fakeAutomationExecutor{outcome: automation.Outcome{
		Deliveries: []automation.Delivery{{URL: "https://app.example.test/setup", Method: "PATCH", Body: `{}`}},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{"language":"javascript","source":"source","event":{"protocol":"imap"}}`)
	request := httptest.NewRequest(http.MethodPost, "/automation/credential-hook", body)
	request.Header.Set("Authorization", "Bearer private-token")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"method":"PATCH"`)) {
		t.Fatalf("credential hook response: %d %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/automation/credential-hook", bytes.NewBufferString(`{"language":"javascript","source":"source","event":{"protocol":"imap"}}`))
	request.Header.Set("Authorization", "Bearer wrong-token")
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("credential hook accepted wrong token: %d", response.Code)
	}
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

func TestHandlerAuthenticatesExtensionOwnedPrivateRoute(t *testing.T) {
	calls := 0
	handler, err := NewHandler(nil, "private-token", WithPrivateRoute("/extension/status", http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusAccepted)
	})))
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(handler, http.MethodPost, "/extension/status", "wrong-token"); response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthorized extension route returned %d after %d calls", response.Code, calls)
	}
	if response := serve(handler, http.MethodPost, "/extension/status", "private-token"); response.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("authenticated extension route returned %d after %d calls", response.Code, calls)
	}
	if _, err := NewHandler(nil, "private-token", WithPrivateRoute("/deliveries/custom", http.NotFoundHandler())); err == nil {
		t.Fatal("reserved private route was accepted")
	}
}

func TestHandlerAuthenticatesDynamicExtensionFallback(t *testing.T) {
	calls := 0
	handler, err := NewHandler(nil, "private-token", WithPrivateFallback(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls++
		if request.URL.Path != "/email/submissions" {
			t.Fatalf("fallback path = %q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusAccepted)
	})))
	if err != nil {
		t.Fatal(err)
	}
	if response := serve(handler, http.MethodPost, "/email/submissions", "wrong-token"); response.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthorized fallback returned %d after %d calls", response.Code, calls)
	}
	if response := serve(handler, http.MethodPost, "/email/submissions", "private-token"); response.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("authenticated fallback returned %d after %d calls", response.Code, calls)
	}
	if response := serve(handler, http.MethodGet, "/deliveries", "private-token"); response.Code != http.StatusNotFound || calls != 1 {
		t.Fatalf("reserved admin path reached fallback: %d after %d calls", response.Code, calls)
	}
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
	if shown := serve(handler, http.MethodGet, "/deliveries/"+deliveryID, "private-token"); shown.Code != http.StatusOK {
		t.Fatalf("unexpected show response: %d", shown.Code)
	}
	if retried := serve(handler, http.MethodPost, "/deliveries/"+deliveryID+"/retry", "private-token"); retried.Code != http.StatusAccepted || queue.retries != 1 {
		t.Fatalf("unexpected retry response: %d %s", retried.Code, retried.Body.String())
	}
}

type adapterQueue struct {
	*fakeQueue
	adapter string
}

func (queue *adapterQueue) SupportsAdapter(name string) bool { return name == queue.adapter }

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

func serve(handler http.Handler, method, path, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
