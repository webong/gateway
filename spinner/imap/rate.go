package imap

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	maxTrackedAuthSources = 10000
	authSourceIdleTTL     = 10 * time.Minute
)

// authLimiter bounds password attempts by the actual socket peer across
// connections. Its scope is one Spinner process, not a distributed account.
type authLimiter struct {
	mu        sync.Mutex
	entries   map[string]authBucket
	perMinute int
	burst     int
	max       int
	now       func() time.Time
	lastSweep time.Time
}

type authBucket struct {
	tokens   float64
	lastSeen time.Time
}

func newAuthLimiter(perMinute, burst int) *authLimiter {
	return &authLimiter{entries: make(map[string]authBucket), perMinute: perMinute,
		burst: burst, max: maxTrackedAuthSources, now: time.Now}
}

func (l *authLimiter) allow(remoteAddr string) bool {
	source := peerIP(remoteAddr)
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	bucket, exists := l.entries[source]
	if !exists {
		if len(l.entries) >= l.max {
			l.sweepIdle(now)
			if len(l.entries) >= l.max {
				return false
			}
		}
		l.entries[source] = authBucket{tokens: float64(l.burst - 1), lastSeen: now}
		return true
	}
	if elapsed := now.Sub(bucket.lastSeen).Seconds(); elapsed > 0 {
		bucket.tokens += elapsed * float64(l.perMinute) / 60
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

func (l *authLimiter) sweepIdle(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < time.Minute {
		return
	}
	for source, bucket := range l.entries {
		if now.Sub(bucket.lastSeen) >= authSourceIdleTTL {
			delete(l.entries, source)
		}
	}
	l.lastSweep = now
}

func peerIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return "unknown"
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	return address.Unmap().String()
}
