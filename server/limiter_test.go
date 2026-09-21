package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenBucketSpendsAndRefills(t *testing.T) {
	l := newLimiter()
	key := uint64(1)
	for i := 0; i < planBurst; i++ {
		if ok, _ := l.allow(key); !ok {
			t.Fatalf("burst of %d should be allowed, failed at %d", planBurst, i)
		}
	}
	ok, retry := l.allow(key)
	if ok {
		t.Error("the bucket should be empty after its burst")
	}
	if retry <= 0 || retry > time.Minute {
		t.Errorf("Retry-After of %v isn't useful advice", retry)
	}
	// Rewind the clock rather than sleeping: a minute of refill should restore the burst.
	l.mu.Lock()
	l.buckets[key].last = time.Now().Add(-time.Minute)
	l.mu.Unlock()
	if ok, _ := l.allow(key); !ok {
		t.Error("a minute later the bucket should have refilled")
	}
}

// One noisy caller must not spend anyone else's budget.
func TestBucketsArePerCaller(t *testing.T) {
	l := newLimiter()
	for i := 0; i < planBurst+5; i++ {
		l.allow(uint64(1))
	}
	if ok, _ := l.allow(uint64(2)); !ok {
		t.Error("a second caller was throttled by the first one's spending")
	}
}

// The key must be derived, not stored: nothing in the limiter should be an address.
func TestClientKeyIsSaltedAndNotTheAddress(t *testing.T) {
	a, b := newLimiter(), newLimiter()
	req := httptest.NewRequest(http.MethodPost, "/v1/plan", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.23, 192.168.1.3")

	k1, k2 := a.clientKey(req), a.clientKey(req)
	if k1 != k2 {
		t.Error("the same caller must hash the same within a process")
	}
	if k1 == b.clientKey(req) {
		t.Error("two processes must not agree on a key: the salt is per-process")
	}
	// The forwarded client, not the proxy, is what gets budgeted.
	other := httptest.NewRequest(http.MethodPost, "/v1/plan", nil)
	other.RemoteAddr = "203.0.113.7:54321"
	other.Header.Set("X-Forwarded-For", "198.51.100.99, 192.168.1.3")
	if a.clientKey(other) == k1 {
		t.Error("two different riders behind the same proxy share a budget")
	}
}

func TestInFlightCapShedsRatherThanQueues(t *testing.T) {
	l := newLimiter()
	for i := 0; i < maxPlansInFlight; i++ {
		if !l.acquire(nil) {
			t.Fatalf("slot %d should have been free", i)
		}
	}
	start := time.Now()
	done := make(chan struct{})
	close(done) // a rider who has already gone away
	if l.acquire(done) {
		t.Error("the machine was full; this should have been shed")
	}
	if time.Since(start) > planWaitForSlot {
		t.Error("a cancelled request shouldn't wait out the whole grace period")
	}
	l.release()
	if !l.acquire(nil) {
		t.Error("a released slot should be reusable")
	}
}

// The endpoint answers 429 with advice, and says so in the counters (ADR-030).
func TestPlanEndpointThrottles(t *testing.T) {
	s := &server{stats: newMetrics(), limits: newLimiter()}
	h := s.limitPlans(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	var lastCode int
	var retryAfter string
	for i := 0; i < planBurst+3; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/plan", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		h(rec, req)
		lastCode, retryAfter = rec.Code, rec.Header().Get("Retry-After")
	}
	if lastCode != http.StatusTooManyRequests {
		t.Fatalf("after %d plans the caller should be throttled, got %d", planBurst+3, lastCode)
	}
	if retryAfter == "" {
		t.Error("a 429 without Retry-After leaves the app guessing")
	}
	if got := s.stats.snapshot("t", true, true, true, "", "").Today.Throttled; got == 0 {
		t.Error("throttling should be visible on the dashboard")
	}
}
