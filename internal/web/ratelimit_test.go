package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRateLimiter_AllowsUpToBurst(t *testing.T) {
	rl := newRateLimiter(1, 5) // 1 token/sec, burst 5
	for i := 0; i < 5; i++ {
		if !rl.allow("ip-a") {
			t.Fatalf("call %d denied; want allowed within burst", i+1)
		}
	}
	// The 6th call within the same instant should be denied.
	if rl.allow("ip-a") {
		t.Fatal("call 6 allowed; want rate-limited after burst")
	}
}

func TestRateLimiter_KeysAreIndependent(t *testing.T) {
	rl := newRateLimiter(1, 1) // 1 token/sec, burst 1
	if !rl.allow("ip-a") {
		t.Fatal("first call from ip-a denied")
	}
	if rl.allow("ip-a") {
		t.Fatal("second call from ip-a allowed; want rate-limited")
	}
	// Different key gets its own bucket.
	if !rl.allow("ip-b") {
		t.Fatal("first call from ip-b denied; per-key buckets should be independent")
	}
}

func TestRateLimit_Middleware_ReturnsRetryAfter(t *testing.T) {
	rl := newRateLimiter(0.0001, 1) // effectively no refill, burst 1
	handler := rateLimit(rl, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// First call passes.
	res1, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", res1.StatusCode)
	}

	// Second call within the same instant should be limited.
	res2, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second call status = %d, want 429", res2.StatusCode)
	}
	if res2.Header.Get("Retry-After") == "" {
		t.Errorf("missing Retry-After header on 429 response")
	}
}

func TestClientIP_PrefersXForwardedFor(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")
	got := clientIP(r)
	if got != "203.0.113.5" {
		t.Errorf("clientIP = %q, want %q", got, "203.0.113.5")
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	r, _ := http.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.10:12345"
	got := clientIP(r)
	if got != "192.168.1.10" {
		t.Errorf("clientIP = %q, want %q", got, "192.168.1.10")
	}
}
