package smtp

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	maxTrackedSources = 10000
	sourceIdleTTL     = 2 * time.Minute
)

// sourceLimiter bounds SMTP MAIL commands per remote IP. It deliberately does
// not trust SMTP-supplied EHLO or envelope addresses as source identities.
type sourceLimiter struct {
	mu                sync.Mutex
	entries           map[string]sourceBucket
	messagesPerMinute int
	burst             int
	maxSources        int
	now               func() time.Time
	lastSweep         time.Time
}

type sourceBucket struct {
	tokens   float64
	lastSeen time.Time
}

func newSourceLimiter(messagesPerMinute, burst int) *sourceLimiter {
	return &sourceLimiter{
		entries:           make(map[string]sourceBucket),
		messagesPerMinute: messagesPerMinute,
		burst:             burst,
		maxSources:        maxTrackedSources,
		now:               time.Now,
	}
}

func (l *sourceLimiter) allow(remoteAddr string) bool {
	source := smtpSourceKey(remoteAddr)

	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	bucket, exists := l.entries[source]
	if !exists {
		if len(l.entries) >= l.maxSources {
			l.sweepIdle(now)
			if len(l.entries) >= l.maxSources {
				return false
			}
		}
		l.entries[source] = sourceBucket{tokens: float64(l.burst - 1), lastSeen: now}
		return true
	}

	elapsed := now.Sub(bucket.lastSeen).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * float64(l.messagesPerMinute) / 60
		if bucket.tokens > float64(l.burst) {
			bucket.tokens = float64(l.burst)
		}
	}
	bucket.lastSeen = now
	allowed := bucket.tokens >= 1
	if allowed {
		bucket.tokens--
	}
	l.entries[source] = bucket
	return allowed
}

func (l *sourceLimiter) sweepIdle(now time.Time) {
	// At capacity, scan at most once a minute. Unknown new sources are denied
	// until an idle entry can be removed, keeping memory and scan work bounded.
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < time.Minute {
		return
	}
	for source, bucket := range l.entries {
		if now.Sub(bucket.lastSeen) >= sourceIdleTTL {
			delete(l.entries, source)
		}
	}
	l.lastSweep = now
}

func smtpSourceKey(remoteAddr string) string {
	if address, ok := smtpPeerIP(remoteAddr); ok {
		return address
	}
	return "unknown"
}

func smtpPeerIP(remoteAddr string) (string, bool) {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return "", false
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "", false
	}
	return address.Unmap().String(), true
}
