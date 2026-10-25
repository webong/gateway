package main

import (
	"net/http"
	"net/http/httptest"
)

type routerHandlerTransport struct{ handler http.Handler }

func (transport routerHandlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	serverRequest := request.Clone(request.Context())
	if serverRequest.Body == nil {
		serverRequest.Body = http.NoBody
	}
	serverRequest.RequestURI = serverRequest.URL.RequestURI()
	serverRequest.RemoteAddr = "127.0.0.1:0"
	if serverRequest.Host == "" {
		serverRequest.Host = serverRequest.URL.Host
	}
	completed := make(chan *http.Response, 1)
	go func() {
		recorder := httptest.NewRecorder()
		transport.handler.ServeHTTP(recorder, serverRequest)
		completed <- recorder.Result()
	}()
	select {
	case response := <-completed:
		return response, nil
	case <-request.Context().Done():
		return nil, request.Context().Err()
	}
}
