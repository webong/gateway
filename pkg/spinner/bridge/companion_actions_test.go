package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webong/gateway/driver/companion"
)

func TestCompanionRunnersShareOnePrivateContract(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get(InternalTokenHeader) != "private-token" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == companionActionsPath:
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"protocol_version": companion.ProtocolVersion,
				"actions":          []map[string]any{{"name": "email.create-ticket", "version": "1.0.0"}},
			})
		case request.Method == http.MethodPost && request.URL.Path == companionActionsPath+"/email.create-ticket/executions":
			var invocation companion.Invocation
			if err := json.NewDecoder(request.Body).Decode(&invocation); err != nil {
				t.Fatalf("decode invocation: %v", err)
			}
			_ = json.NewEncoder(writer).Encode(companion.Result{
				ProtocolVersion: companion.ProtocolVersion,
				ExecutionID:     invocation.ExecutionID,
				Status:          companion.StatusSucceeded,
				Output:          map[string]any{"ticket_id": "ticket-1"},
			})
		default:
			http.NotFound(writer, request)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	httpRunner, err := NewHTTPCompanionRunner(server.URL, server.Client(), 4096, "private-token")
	if err != nil {
		t.Fatalf("create HTTP runner: %v", err)
	}
	phpRunner, err := NewPHPCompanionRunner(handler, 4096, "private-token")
	if err != nil {
		t.Fatalf("create in-process runner: %v", err)
	}

	for name, runner := range map[string]companion.Runner{"http": httpRunner, "in-process": phpRunner} {
		t.Run(name, func(t *testing.T) {
			capabilities, err := runner.Capabilities(context.Background())
			if err != nil || len(capabilities) != 1 || capabilities[0].Name != "email.create-ticket" {
				t.Fatalf("unexpected capabilities: %+v, %v", capabilities, err)
			}
			result, err := runner.Invoke(context.Background(), companion.Invocation{
				ProtocolVersion:  companion.ProtocolVersion,
				Action:           "email.create-ticket",
				CompanionVersion: "1.0.0",
				ExecutionID:      "execution-1",
				IdempotencyKey:   "message-1",
				Input:            map[string]any{"subject": "Help"},
			})
			if err != nil || result.Status != companion.StatusSucceeded || result.Output["ticket_id"] != "ticket-1" {
				t.Fatalf("unexpected result: %+v, %v", result, err)
			}
		})
	}
}

func TestCompanionRunnerRejectsPlannerErrorsAndOversizedDocuments(t *testing.T) {
	for name, handler := range map[string]http.Handler{
		"private error": http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"message":"Companion version mismatch."}`))
		}),
		"oversized response": http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = writer.Write([]byte(`{"protocol_version":"v1","actions":[{"name":"email.create-ticket","version":"` + string(make([]byte, 128)) + `"}]}`))
		}),
	} {
		t.Run(name, func(t *testing.T) {
			runner, err := NewPHPCompanionRunner(handler, 64, "private-token")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runner.Capabilities(context.Background()); err == nil {
				t.Fatal("expected capability request to fail")
			}
		})
	}
}
