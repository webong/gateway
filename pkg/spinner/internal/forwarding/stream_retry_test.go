package forwarding

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/webong/gateway/pkg/spinner/internal/config"
	"github.com/webong/gateway/pkg/spinner/internal/logging"
)

func TestStreamRetryRequiresOptInAndPreservesRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		retry   bool
		status  int
		want    int
		success bool
	}{
		{"disabled", false, 0, 1, false}, {"enabled", true, 0, 2, true}, {"response received", true, 503, 1, true},
		{"retry exhausted", true, -1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if string(body) != "input" || r.Header.Get("Idempotency-Key") != "request-1" {
					t.Error("replay changed body or key")
				}
				if calls.Add(1) == 1 || tc.status == -1 {
					if tc.status > 0 {
						w.WriteHeader(tc.status)
						return
					}
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: done\n\n")
			}))
			defer s.Close()
			f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
			f.addressAllowed = func(net.IP) bool { return true }
			defer f.CloseIdleConnections()
			resp, err := f.OpenStreamRequestPolicy(context.Background(), "POST", s.URL, http.Header{"Idempotency-Key": {"request-1"}}, []byte("input"), HTTPTransport{}, tc.retry)
			if resp != nil {
				resp.Body.Close()
			}
			if (err == nil) != tc.success || int(calls.Load()) != tc.want {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
		})
	}
}

func TestStreamRetryRejectsMissingKey(t *testing.T) {
	f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
	defer f.CloseIdleConnections()
	if _, err := f.OpenStreamRequestPolicy(context.Background(), "POST", "https://source.test", nil, []byte("input"), HTTPTransport{}, true); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestContinuousUploadForwardsIncrementally(t *testing.T) {
	first := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 5)
		if _, err := io.ReadFull(r.Body, b); err != nil {
			t.Error(err)
			return
		}
		close(first)
		tail, err := io.ReadAll(r.Body)
		if err != nil || string(b)+string(tail) != "firstsecond" {
			t.Errorf("body=%q%q %v", b, tail, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: done\n\n")
	}))
	defer s.Close()
	f := NewForwarder(&config.Config{RequestTimeout: time.Second}, logging.NewLogger("error"))
	f.addressAllowed = func(net.IP) bool { return true }
	defer f.CloseIdleConnections()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		w.Write([]byte("first"))
		select {
		case <-first:
		case <-ctx.Done():
		}
		w.Write([]byte("second"))
		w.Close()
	}()
	resp, err := f.OpenStreamUpload(ctx, s.URL, nil, r, HTTPTransport{})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
