package pop3

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"sync/atomic"
	"testing"
	"time"

	bridge "github.com/webong/gateway/arranger/protocol"
)

func TestAuthLimiterRefillsNormalizesAndBoundsSources(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newAuthLimiter(2, 2)
	limiter.now = func() time.Time { return now }
	if !limiter.allow("192.0.2.1:1000") || !limiter.allow("[::ffff:192.0.2.1]:1001") || limiter.allow("192.0.2.1:1002") {
		t.Fatal("attempts from one normalized source did not share a burst")
	}
	if !limiter.allow("192.0.2.2:1000") {
		t.Fatal("independent source was throttled")
	}
	now = now.Add(30 * time.Second)
	if !limiter.allow("192.0.2.1:1003") || limiter.allow("192.0.2.1:1004") {
		t.Fatal("token refill was incorrect")
	}
	limiter.max = 2
	if limiter.allow("192.0.2.3:1000") {
		t.Fatal("new source exceeded the tracking bound")
	}
	now = now.Add(authSourceIdleTTL)
	if !limiter.allow("192.0.2.3:1000") || len(limiter.entries) != 1 {
		t.Fatal("idle sources were not replaced")
	}
}

func TestPOP3LimitsPasswordAttemptsAcrossConnectionsBeforePlanner(t *testing.T) {
	tlsConfig, roots := testTLS(t)
	var plannerCalls atomic.Int32
	planner := plannerFunc(func(_ context.Context, event bridge.GatewayEvent) (bridge.GatewayDecision, error) {
		plannerCalls.Add(1)
		return bridge.GatewayDecision{Version: bridge.ProtocolVersion, Protocol: bridge.ProtocolPOP3, Action: bridge.GatewayReject}, nil
	})
	server, err := NewServer(Config{
		Address: "127.0.0.1:0", TLSConfig: tlsConfig, AuthPerMinute: 1, AuthBurst: 1,
		Upstreams: map[string]Upstream{"primary": {Address: "127.0.0.1:995", ServerName: "localhost"}},
	}, planner)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	defer func() { _ = server.Shutdown(context.Background()); <-served }()

	attempt := func(expected string) {
		client, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: "localhost"})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		reader := bufio.NewReader(client)
		expectLine(t, reader, "+OK Gateway POP3 ready")
		_, _ = client.Write([]byte("USER alice\r\n"))
		expectLine(t, reader, "+OK username received")
		_, _ = client.Write([]byte("PASS secret\r\n"))
		expectLine(t, reader, expected)
	}
	attempt("-ERR authentication failed")
	attempt("-ERR authentication rate limit exceeded")
	if plannerCalls.Load() != 1 {
		t.Fatalf("Planner calls = %d, want 1", plannerCalls.Load())
	}
}
