package webstream

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	bridge "github.com/webong/gateway/arranger/integration"
)

func TestUploadResponseArrivesBeforeUploadCompletes(t *testing.T) {
	planner := plannerFunc(func(_ context.Context, e bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Attributes: map[string]string{"method": "POST", "upload_mode": "stream"}}}, nil
	})
	source := uploadSource{open: func(ctx context.Context, d bridge.GatewayDelivery, body io.ReadCloser) (*http.Response, error) {
		r, w := io.Pipe()
		go func() {
			defer w.Close()
			b := make([]byte, 5)
			if _, err := io.ReadFull(body, b); err != nil {
				w.CloseWithError(err)
				return
			}
			io.WriteString(w, "data: "+string(b)+"\n\n")
			b, err := io.ReadAll(body)
			if err != nil {
				w.CloseWithError(err)
				return
			}
			io.WriteString(w, "data: "+string(b)+"\n\n")
		}()
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: r}, nil
	}}
	s := httptest.NewServer(NewHandler(planner, source, Config{}))
	defer s.Close()
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", s.URL, r)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Gateway-Upload-Mode", "stream")
	go func() { w.Write([]byte("first")) }()
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	line, err := reader.ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("first event=%q %v", line, err)
	}
	w.Write([]byte("second"))
	w.Close()
	tail, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(tail), "data: second") {
		t.Fatalf("tail=%q %v", tail, err)
	}
}

func gzipBytes(s string) []byte {
	var b bytes.Buffer
	w := gzip.NewWriter(&b)
	w.Write([]byte(s))
	w.Close()
	return b.Bytes()
}

func TestGzipBodyLimitsAndValidation(t *testing.T) {
	valid := gzipBytes("hello")
	corrupt := append([]byte(nil), valid...)
	corrupt[len(corrupt)-8] ^= 0xff
	for _, tc := range []struct {
		name     string
		body     []byte
		encoding string
		limit    int64
		status   int
	}{
		{"valid", valid, "gzip", 100, 200},
		{"empty", gzipBytes(""), "gzip", 100, 200},
		{"expanded limit", gzipBytes(strings.Repeat("x", 101)), "gzip", 100, 413},
		{"wire limit", valid, "gzip", 10, 413},
		{"truncated", valid[:len(valid)-4], "gzip", 100, 400},
		{"checksum", corrupt, "gzip", 100, 400},
		{"stacked", valid, "gzip, gzip", 100, 415},
		{"unsupported", valid, "br", 100, 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			planned := false
			h := NewHandler(plannerFunc(func(_ context.Context, e bridge.GatewayEvent) (bridge.GatewayDecision, error) {
				planned = true
				if e.Headers["Content-Encoding"] != nil || e.Headers["Content-Length"] != nil {
					t.Error("stale body metadata")
				}
				if string(e.Payload) != "hello" && len(e.Payload) != 0 {
					t.Error("not decoded")
				}
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayRespond, StatusCode: 200}, nil
			}), sourceFunc(func(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
				t.Fatal("unexpected source")
				return nil, nil
			}), Config{MaxBodySize: tc.limit})
			r := httptest.NewRequest("POST", "/events", bytes.NewReader(tc.body))
			r.Header.Set("Content-Encoding", tc.encoding)
			r.Header.Set("Accept", "text/event-stream")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || planned != (tc.status == 200) {
				t.Fatalf("status=%d planned=%v", w.Code, planned)
			}
		})
	}
}

type uploadSource struct {
	open func(context.Context, bridge.GatewayDelivery, io.ReadCloser) (*http.Response, error)
}

func (s uploadSource) OpenWebstream(context.Context, bridge.GatewayDelivery) (*http.Response, error) {
	panic("buffered source called")
}
func (s uploadSource) OpenWebstreamUpload(c context.Context, d bridge.GatewayDelivery, b io.ReadCloser) (*http.Response, error) {
	return s.open(c, d, b)
}

func TestStreamingUploadAuthorizedBeforeBodyRead(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		planned := make(chan struct{})
		planner := plannerFunc(func(_ context.Context, e bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			if len(e.Payload) != 0 || e.Attributes["upload_mode"] != "stream" {
				t.Error("not metadata authorization")
			}
			close(planned)
			if !allowed {
				return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayReject, StatusCode: 403}, nil
			}
			return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolWebstream, Action: bridge.GatewayStream, Stream: &bridge.GatewayDelivery{Protocol: bridge.ProtocolHTTP, Target: "https://source.test", Attributes: map[string]string{"method": "POST", "upload_mode": "stream"}}}, nil
		})
		source := uploadSource{open: func(ctx context.Context, d bridge.GatewayDelivery, body io.ReadCloser) (*http.Response, error) {
			if !allowed {
				t.Error("unauthorized source")
			}
			b, err := io.ReadAll(body)
			if err != nil || string(b) != "firstsecond" {
				t.Errorf("upload=%q %v", b, err)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: done\n\n"))}, nil
		}}
		server := httptest.NewServer(NewHandler(planner, source, Config{}))
		r, w := io.Pipe()
		req, _ := http.NewRequest("POST", server.URL, r)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("X-Gateway-Upload-Mode", "stream")
		go func() { <-planned; w.Write([]byte("first")); w.Write([]byte("second")); w.Close() }()
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		r.Close()
		w.Close()
		server.Close()
		want := 403
		if allowed {
			want = 200
		}
		if resp.StatusCode != want {
			t.Fatalf("status=%d", resp.StatusCode)
		}
	}
}
