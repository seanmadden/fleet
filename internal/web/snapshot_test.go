package web

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSessionSnapshot_JSONRoundTrip(t *testing.T) {
	created := time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC)
	accessed := created.Add(time.Hour)
	in := SessionSnapshot{
		ID:              "abc",
		Title:           "test",
		ProjectPath:     "/repo",
		Status:          "running",
		WorkspaceName:   "feature/x",
		CreatedAt:       created,
		LastAccessedAt:  accessed,
		ClaudeSessionID: "claude-1",
		FirstPrompt:     "hello",
		PromptCount:     3,
		IsAlive:         true,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SessionSnapshot
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch:\n in: %+v\nout: %+v", in, out)
	}
}

func TestSessionEvent_JSONRoundTrip(t *testing.T) {
	snap := SessionSnapshot{ID: "id1", Title: "t", Status: "waiting", IsAlive: true}
	evt := SessionEvent{
		Kind:      "updated",
		SessionID: "id1",
		Snapshot:  &snap,
		Timestamp: time.Date(2026, 5, 21, 10, 0, 0, 0, time.UTC),
	}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SessionEvent
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Kind != evt.Kind || out.SessionID != evt.SessionID || out.Snapshot == nil || *out.Snapshot != snap {
		t.Fatalf("round-trip mismatch:\n in: %+v\nout: %+v", evt, out)
	}
}

func TestSessionEvent_RefreshNoSnapshot(t *testing.T) {
	// "refresh" markers have no per-session payload; ensure they marshal
	// without panicking on nil Snapshot and round-trip cleanly.
	evt := SessionEvent{Kind: "refresh", Timestamp: time.Now()}
	raw, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out SessionEvent
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Kind != "refresh" || out.Snapshot != nil {
		t.Fatalf("unexpected snapshot or kind: %+v", out)
	}
}
