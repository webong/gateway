package rpc

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestServeWaitsForInflightRPCResponseDuringShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":37,"result":{"committed":true}}`)
	})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, server, listener, time.Second) }()
	response := make(chan string, 1)
	go func() {
		r, e := http.Get("http://" + listener.Addr().String())
		if e != nil {
			response <- "error"
			return
		}
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		response <- string(body)
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		close(release)
		t.Fatalf("bridge returned before active response finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case body := <-response:
		if body != `{"jsonrpc":"2.0","id":37,"result":{"committed":true}}` {
			t.Fatalf("truncated RPC acknowledgement: %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("response did not drain")
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish")
	}
}

func TestServeBoundsDrainAndClosesStalledRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	exited := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-r.Context().Done(); close(exited) })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, server, listener, 20*time.Millisecond) }()
	go func() {
		response, _ := http.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("drain result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("unbounded drain")
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("stalled HTTP request survived close")
	}
}
