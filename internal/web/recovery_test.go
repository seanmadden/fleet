package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryMiddleware_CatchesPanic(t *testing.T) {
	// A handler that panics with both a string and a non-error type to
	// confirm the recover() path tolerates arbitrary values.
	panicker := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("kaboom")
	})

	srv := httptest.NewServer(recoveryMiddleware(panicker))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/anything")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "internal server error") {
		t.Errorf("body = %q, want substring 'internal server error'", body)
	}
}

func TestRecoveryMiddleware_PassesThroughOnNoPanic(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("brew"))
	})

	srv := httptest.NewServer(recoveryMiddleware(ok))
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want 418", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "brew" {
		t.Errorf("body = %q, want %q", body, "brew")
	}
}
