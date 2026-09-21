package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rate limiting, sized from a load test rather than a guess (ADR-030).
//
// Measured on axiom, 21 Sep 2026: a 260 km loop costs about 1.3 core-seconds, 16 concurrent plans
// put GraphHopper at 1280 % CPU (12.8 of 16 cores) with a p95 of 1.5 s, and 32 concurrent gave
// 16 plans/s at a p95 of 2.8 s with no errors. It degrades linearly rather than collapsing, so the
// job here is to keep latency honest under a crowd and to stop one client taking the machine.
//
// Two limits: how many expensive plans run at once, and how many a single caller may ask for. Both
// shed load with 429 and Retry-After rather than queueing for minutes — a rider would rather be told
// to try again than watch a spinner until the app times out.
const (
	maxPlansInFlight = 24              // ~1.5x the measured saturation point; beyond this, latency is a lie
	planWaitForSlot  = 2 * time.Second // brief queue before shedding: absorbs a burst without hiding one
	plansPerMinute   = 20              // a rider plans a handful of rides; a script plans thousands
	planBurst        = 8
	limiterIdleAfter = 15 * time.Minute
)

// bucket is a token bucket per caller. Keys are salted hashes, never addresses (see clientKey).
type tokenBucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu      sync.Mutex
	buckets map[uint64]*tokenBucket
	salt    [16]byte

	slots chan struct{}

	perMinute float64
	burst     float64
}

func newLimiter() *limiter {
	l := &limiter{
		buckets:   map[uint64]*tokenBucket{},
		slots:     make(chan struct{}, maxPlansInFlight),
		perMinute: plansPerMinute,
		burst:     planBurst,
	}
	_, _ = rand.Read(l.salt[:])
	go l.evictIdle()
	return l
}

// clientKey identifies a caller without keeping their address. The salt is fresh per process, so the
// keys are meaningless outside this run and nothing here can be turned back into an IP (ADR-017's
// spirit: we don't keep what we don't need).
func (l *limiter) clientKey(r *http.Request) uint64 {
	ip := r.RemoteAddr
	// Caddy is the only thing that talks to us, and it sets X-Forwarded-For; the leftmost entry is
	// the rider. It's spoofable, which for rate limiting means an abuser can spread themselves out —
	// the in-flight cap is what actually protects the machine.
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		if first, _, ok := strings.Cut(fwd, ","); ok {
			ip = first
		} else {
			ip = fwd
		}
	}
	if host, _, err := net.SplitHostPort(strings.TrimSpace(ip)); err == nil {
		ip = host
	}
	sum := sha256.Sum256(append(l.salt[:], strings.TrimSpace(ip)...))
	return binary.BigEndian.Uint64(sum[:8])
}

// allow spends a token for this caller, reporting how long until the next one if empty.
func (l *limiter) allow(key uint64) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * l.perMinute
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	need := (1 - b.tokens) / l.perMinute
	return false, time.Duration(need * float64(time.Minute))
}

// acquire takes an in-flight slot, waiting briefly for one. The boolean says whether to proceed.
func (l *limiter) acquire(done <-chan struct{}) bool {
	select {
	case l.slots <- struct{}{}:
		return true
	default:
	}
	t := time.NewTimer(planWaitForSlot)
	defer t.Stop()
	select {
	case l.slots <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-done:
		return false
	}
}

func (l *limiter) release() {
	select {
	case <-l.slots:
	default:
	}
}

func (l *limiter) evictIdle() {
	for range time.Tick(limiterIdleAfter) {
		cutoff := time.Now().Add(-limiterIdleAfter)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.last.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// limitPlans wraps the expensive endpoint: per-caller budget first (cheap to check), then a slot.
func (s *server) limitPlans(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.limits == nil {
			next(w, r)
			return
		}
		if ok, retry := s.limits.allow(s.limits.clientKey(r)); !ok {
			s.stats.failure("throttled")
			w.Header().Set("Retry-After", retryAfterSeconds(retry))
			writeError(w, http.StatusTooManyRequests,
				"you're planning faster than we can ride; try again in a moment")
			return
		}
		if !s.limits.acquire(r.Context().Done()) {
			s.stats.failure("busy")
			w.Header().Set("Retry-After", "5")
			writeError(w, http.StatusTooManyRequests, "busy planning other rides; try again in a few seconds")
			return
		}
		defer s.limits.release()
		next(w, r)
	}
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds()) + 1
	if secs < 1 {
		secs = 1
	}
	return strconv.Itoa(secs)
}
