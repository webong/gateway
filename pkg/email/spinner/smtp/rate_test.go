package smtp

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	smtpclient "github.com/emersion/go-smtp"
	bridge "github.com/webong/gateway/arranger/integration"
)

func TestSourceLimiterRefillsAndSeparatesIPs(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newSourceLimiter(2, 2)
	limiter.now = func() time.Time { return now }
	if !limiter.allow("192.0.2.10:1000") || !limiter.allow("[::ffff:192.0.2.10]:1001") {
		t.Fatal("expected two tokens for the same normalized source")
	}
	if limiter.allow("192.0.2.10:1002") {
		t.Fatal("source exceeded its burst")
	}
	if !limiter.allow("192.0.2.11:1000") {
		t.Fatal("a separate source was throttled")
	}
	now = now.Add(30 * time.Second)
	if !limiter.allow("192.0.2.10:1003") || limiter.allow("192.0.2.10:1004") {
		t.Fatal("source did not refill at two messages per minute")
	}
}

func TestSMTPPeerIPUsesOnlySocketAddress(t *testing.T) {
	for _, test := range []struct {
		remote string
		want   string
		valid  bool
	}{
		{"192.0.2.10:2525", "192.0.2.10", true},
		{"[2001:db8::1]:2525", "2001:db8::1", true},
		{"[::ffff:192.0.2.10]:2525", "192.0.2.10", true},
		{"untrusted.example:2525", "", false},
		{"192.0.2.10", "", false},
	} {
		got, valid := smtpPeerIP(test.remote)
		if got != test.want || valid != test.valid {
			t.Errorf("smtpPeerIP(%q) = %q, %t; want %q, %t", test.remote, got, valid, test.want, test.valid)
		}
	}
}

func TestSourceLimiterBoundsTrackedSources(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := newSourceLimiter(1, 1)
	limiter.maxSources = 1
	limiter.now = func() time.Time { return now }
	if !limiter.allow("192.0.2.10:1000") || limiter.allow("192.0.2.11:1000") {
		t.Fatal("new source was not denied when tracking capacity was full")
	}
	now = now.Add(sourceIdleTTL)
	if !limiter.allow("192.0.2.11:1000") || len(limiter.entries) != 1 {
		t.Fatal("idle source was not replaced within tracking capacity")
	}
}

func TestSMTPRateLimitRejectsMAILAcrossConnections(t *testing.T) {
	var plannerCalls atomic.Int32
	server, err := NewServer(Config{Address: "127.0.0.1:0", MessagesPerMinute: 1, MessageBurst: 1},
		plannerFunc(func(context.Context, bridge.GatewayEvent) (bridge.GatewayDecision, error) {
			plannerCalls.Add(1)
			return bridge.GatewayDecision{}, nil
		}), &recordingExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil && !errors.Is(err, ErrServerClosed) {
			t.Errorf("shutdown SMTP server: %v", err)
		}
		select {
		case err := <-serveErr:
			if err != nil && !errors.Is(err, ErrServerClosed) {
				t.Errorf("serve SMTP server: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("SMTP server did not stop")
		}
	})

	first, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.Hello("client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := first.Mail("sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := first.Reset(); err != nil {
		t.Fatal(err)
	}
	var smtpErr *smtpclient.SMTPError
	if err := first.Mail("sender@example.test", nil); !errors.As(err, &smtpErr) || smtpErr.Code != 451 || smtpErr.EnhancedCode != (smtpclient.EnhancedCode{4, 7, 1}) {
		t.Fatalf("expected temporary source rate limit, got %v", err)
	}

	second, err := smtpclient.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := second.Hello("other-client.example.test"); err != nil {
		t.Fatal(err)
	}
	if err := second.Mail("sender@example.test", nil); !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
		t.Fatalf("expected source rate limit across connections, got %v", err)
	}
	if plannerCalls.Load() != 0 {
		t.Fatal("rate-limited MAIL called Planner")
	}
}
