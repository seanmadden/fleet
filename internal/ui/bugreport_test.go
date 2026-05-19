package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBugReportDialog_Esc_Hides(t *testing.T) {
	d := NewBugReportDialog()
	d.visible = true

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if d.visible {
		t.Fatal("expected dialog to be hidden after esc")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from esc")
	}
	msg := cmd()
	if _, ok := msg.(bugReportClosedMsg); !ok {
		t.Fatalf("expected bugReportClosedMsg, got %T", msg)
	}
}

func TestBugReportDialog_OtherKey_Noop(t *testing.T) {
	d := NewBugReportDialog()
	d.visible = true

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if !d.visible {
		t.Fatal("expected dialog to remain visible on non-esc key")
	}
	if cmd != nil {
		t.Fatal("expected nil cmd from non-esc key")
	}
}
