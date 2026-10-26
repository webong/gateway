// Package plannerclient adapts an Extension's authenticated private Planner
// handler to the standard HTTP client used by Email's typed control adapters.
package plannerclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
)

const Origin = "http://gateway.internal"

func New(handler http.Handler) (*http.Client, error) {
	if handler == nil {
		return nil, errors.New("private Planner handler is required")
	}
	return &http.Client{
		Transport:     handlerTransport{handler: handler},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	response.Request = request
	return response, nil
}
