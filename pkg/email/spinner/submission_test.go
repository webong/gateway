package spinner

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmissionHandlerKeepsCredentialsInsideEmailEntrypoint(t *testing.T) {
	planned := false
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { planned = true })
	planner := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Basic private" {
			t.Error("submission credentials were lost")
		}
		writer.WriteHeader(http.StatusAccepted)
	})
	handler := SubmissionHandler(next, planner, 4)
	request := httptest.NewRequest(http.MethodPost, SubmissionPath, strings.NewReader(strings.Repeat("x", 20)))
	request.Header.Set("Authorization", "Basic private")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if planned || response.Code != http.StatusAccepted {
		t.Fatalf("email credentials escaped the Email entrypoint: planned=%v status=%d", planned, response.Code)
	}
}

func TestSubmissionHandlerRejectsOversizedEnvelope(t *testing.T) {
	called := false
	planner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
	handler := SubmissionHandler(http.NotFoundHandler(), planner, 1)
	request := httptest.NewRequest(http.MethodPost, SubmissionPath, strings.NewReader(strings.Repeat("x", 65539)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if called || response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized Email envelope was forwarded: called=%v status=%d", called, response.Code)
	}
}
