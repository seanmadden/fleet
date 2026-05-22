package web

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Fakes ---

type fakeSource struct {
	mu       sync.Mutex
	sessions []SessionSnapshot
	panes    map[string]string
	paneErr  map[string]error
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		panes:   make(map[string]string),
		paneErr: make(map[string]error),
	}
}

func (f *fakeSource) SessionsSnapshot() []SessionSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SessionSnapshot, len(f.sessions))
	copy(out, f.sessions)
	return out
}

func (f *fakeSource) PaneSnapshot(id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.paneErr[id]; ok {
		return "", err
	}
	if p, ok := f.panes[id]; ok {
		return p, nil
	}
	return "", fmt.Errorf("not found: %s", id)
}

type fakeDispatcher struct {
	mu        sync.Mutex
	calls     []Mutation
	autoReply error
	autoSleep time.Duration
}

func (f *fakeDispatcher) Dispatch(m Mutation) {
	f.mu.Lock()
	f.calls = append(f.calls, m)
	delay := f.autoSleep
	reply := f.autoReply
	f.mu.Unlock()
	if m.Reply != nil {
		go func() {
			if delay > 0 {
				time.Sleep(delay)
			}
			m.Reply <- reply
		}()
	}
}

func (f *fakeDispatcher) callsCopy() []Mutation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Mutation, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- Helpers ---

const testToken = "test-token-abc123"

func newTestServer(t *testing.T, src SessionSource, disp MutationDispatcher) (*Server, *httptest.Server) {
	t.Helper()
	srv := &Server{
		deps: Deps{
			Source:          src,
			Dispatcher:      disp,
			Addr:            "127.0.0.1:0",
			Token:           testToken,
			MutationTimeout: 500 * time.Millisecond,
		},
		hub:   newEventHub(),
		token: testToken,
	}
	srv.http = &http.Server{Handler: srv.buildHandler()}
	ts := httptest.NewServer(srv.http.Handler)
	t.Cleanup(ts.Close)
	return srv, ts
}

func req(t *testing.T, method, url, token string, body any) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	r, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, url, err)
	}
	return res
}

// --- Constructor ---

func TestNewServer_RejectsMissingDeps(t *testing.T) {
	cases := []struct {
		name string
		d    Deps
	}{
		{"no source", Deps{Dispatcher: &fakeDispatcher{}, Addr: "127.0.0.1:0", Token: "t"}},
		{"no dispatcher", Deps{Source: newFakeSource(), Addr: "127.0.0.1:0", Token: "t"}},
		{"no addr", Deps{Source: newFakeSource(), Dispatcher: &fakeDispatcher{}, Token: "t"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewServer(c.d); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestNewServer_NonLoopbackWithoutTokenRefused(t *testing.T) {
	_, err := NewServer(Deps{
		Source:     newFakeSource(),
		Dispatcher: &fakeDispatcher{},
		Addr:       "0.0.0.0:8765",
		Token:      "",
	})
	if err == nil {
		t.Fatal("expected error for empty token on non-loopback addr")
	}
}

func TestNewServer_LoopbackWithoutTokenAllowed(t *testing.T) {
	s, err := NewServer(Deps{
		Source:     newFakeSource(),
		Dispatcher: &fakeDispatcher{},
		Addr:       "127.0.0.1:0",
		Token:      "",
	})
	if err != nil {
		t.Fatalf("expected nil error for loopback addr with empty token; got %v", err)
	}
	if s.Token() != "" {
		t.Errorf("Token() = %q, want empty", s.Token())
	}
}

// --- Auth ---

func TestAuth_MissingTokenReturns401(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestAuth_WrongTokenReturns401(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions", "nope", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

func TestAuth_CorrectTokenAllows(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

func TestAuth_SSEAcceptsQueryParam(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	// EventSource can't set headers — token via ?token=
	r, err := http.NewRequest("GET", ts.URL+"/api/events?token="+testToken, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	r = r.WithContext(ctx)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		// timeout is fine — connection succeeded means we got past auth
		if strings.Contains(err.Error(), "context deadline exceeded") {
			return
		}
		t.Fatalf("do: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
}

func TestAuth_SSEMissingTokenReturns401(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/events", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", res.StatusCode)
	}
}

// --- Handlers ---

func TestListSessions(t *testing.T) {
	src := newFakeSource()
	src.sessions = []SessionSnapshot{
		{ID: "a", Title: "alpha", Status: "running", IsAlive: true},
		{ID: "b", Title: "beta", Status: "waiting", IsAlive: true},
	}
	_, ts := newTestServer(t, src, &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	var got []SessionSnapshot
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[1].Title != "beta" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestGetPane_OK(t *testing.T) {
	src := newFakeSource()
	src.panes["abc"] = "pane-content-here"
	_, ts := newTestServer(t, src, &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions/abc/pane", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "pane-content-here" {
		t.Fatalf("body = %q", body)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want text/plain", ct)
	}
}

func TestGetPane_NotFound(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "GET", ts.URL+"/api/sessions/missing/pane", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
}

func TestSendKeys_DispatchesMutation(t *testing.T) {
	disp := &fakeDispatcher{}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions/abc/sendkeys", testToken, map[string]string{"keys": "Enter"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	calls := disp.callsCopy()
	if len(calls) != 1 || calls[0].Kind != MutationSendKeys || calls[0].ID != "abc" {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if k, _ := calls[0].Payload["keys"].(string); k != "Enter" {
		t.Errorf("payload keys = %q, want Enter", k)
	}
}

func TestSendKeys_MissingKeysReturns400(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "POST", ts.URL+"/api/sessions/abc/sendkeys", testToken, map[string]string{})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

func TestApprove_DispatchesMutation(t *testing.T) {
	disp := &fakeDispatcher{}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions/abc/approve", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
	calls := disp.callsCopy()
	if len(calls) != 1 || calls[0].Kind != MutationApprove {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestRestart_DispatchesMutation(t *testing.T) {
	disp := &fakeDispatcher{}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions/abc/restart", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
	calls := disp.callsCopy()
	if len(calls) != 1 || calls[0].Kind != MutationRestart {
		t.Fatalf("unexpected calls: %+v", calls)
	}
}

func TestDelete_DispatchesMutationWithFlags(t *testing.T) {
	disp := &fakeDispatcher{}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions/abc/delete", testToken, map[string]bool{"destroyWorkspace": true, "unpinRepo": false})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", res.StatusCode)
	}
	calls := disp.callsCopy()
	if len(calls) != 1 || calls[0].Kind != MutationDelete {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if v, _ := calls[0].Payload["destroyWorkspace"].(bool); !v {
		t.Errorf("destroyWorkspace not propagated: %+v", calls[0].Payload)
	}
}

func TestCreateSession_DispatchesMutation(t *testing.T) {
	disp := &fakeDispatcher{}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions", testToken, map[string]string{
		"title": "hi", "path": "/tmp/repo", "workspaceName": "feature/x",
	})
	defer res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("status = %d, body = %s", res.StatusCode, body)
	}
	calls := disp.callsCopy()
	if len(calls) != 1 || calls[0].Kind != MutationCreate {
		t.Fatalf("unexpected calls: %+v", calls)
	}
	if p, _ := calls[0].Payload["path"].(string); p != "/tmp/repo" {
		t.Errorf("payload path = %q", p)
	}
}

func TestCreateSession_RequiresPath(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	res := req(t, "POST", ts.URL+"/api/sessions", testToken, map[string]string{"title": "hi"})
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

// TestMutationTimeout — a dispatcher that never replies should cause the
// handler to return 504 after MutationTimeout (set to 500ms in newTestServer).
func TestMutationTimeout(t *testing.T) {
	srv := &Server{
		deps: Deps{
			Source:          newFakeSource(),
			Dispatcher:      &silentDispatcher{},
			Addr:            "127.0.0.1:0",
			Token:           testToken,
			MutationTimeout: 50 * time.Millisecond,
		},
		hub:   newEventHub(),
		token: testToken,
	}
	srv.http = &http.Server{Handler: srv.buildHandler()}
	ts := httptest.NewServer(srv.http.Handler)
	defer ts.Close()
	res := req(t, "POST", ts.URL+"/api/sessions/abc/approve", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504", res.StatusCode)
	}
}

type silentDispatcher struct{}

func (silentDispatcher) Dispatch(_ Mutation) {} // never replies

// TestMutationErrorPropagates — a dispatcher that returns an error should
// produce a 500 with the error message in the body.
func TestMutationErrorPropagates(t *testing.T) {
	disp := &fakeDispatcher{autoReply: fmt.Errorf("session not waiting for approval")}
	_, ts := newTestServer(t, newFakeSource(), disp)
	res := req(t, "POST", ts.URL+"/api/sessions/abc/approve", testToken, nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(body), "not waiting") {
		t.Errorf("body = %q, want substring 'not waiting'", body)
	}
}

// --- SSE ---

func TestSSE_DeliversInitialRefresh(t *testing.T) {
	srv, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})

	// Connect.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	r.Header.Set("Authorization", "Bearer "+testToken)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	// Read until we see the initial refresh.
	br := bufio.NewReader(res.Body)
	var saw bool
	for !saw {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read line: %v", err)
		}
		if strings.HasPrefix(line, "event: refresh") {
			saw = true
		}
	}

	// Publish an event and ensure we receive it.
	srv.Publish(SessionEvent{Kind: "created", SessionID: "id1"})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.HasPrefix(line, "event: created") {
			return // success
		}
	}
	t.Fatal("did not receive 'created' event")
}

func TestSSE_DropsOnSlowSubscriber(t *testing.T) {
	hub := newEventHub()
	sub := hub.subscribe()
	defer hub.unsubscribe(sub)

	// Fill the buffer plus one to force a drop.
	for i := 0; i < subscriberBuffer+1; i++ {
		hub.publish(SessionEvent{Kind: "updated", SessionID: fmt.Sprintf("id%d", i)})
	}
	if !sub.takePendingRefresh() {
		t.Fatal("expected pendingRefresh after dropping events")
	}
	// Second take resets the flag.
	if sub.takePendingRefresh() {
		t.Fatal("expected pendingRefresh to be cleared after first take")
	}
}

// --- Loopback detection ---

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"127.0.0.1:8765", true},
		{"localhost:8765", true},
		{"[::1]:8765", true},
		{"0.0.0.0:8765", false},
		{"192.168.1.5:8765", false},
		{"example.com:8765", false},
	}
	for _, c := range cases {
		got := isLoopbackAddr(c.in)
		if got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// --- Static assets ---

func TestStatic_ServesIndex(t *testing.T) {
	_, ts := newTestServer(t, newFakeSource(), &fakeDispatcher{})
	// Static is unauthenticated.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if !bytes.Contains(body, []byte("fleet")) {
		n := len(body)
		if n > 200 {
			n = 200
		}
		t.Errorf("body did not contain 'fleet'; got %q", body[:n])
	}
}
