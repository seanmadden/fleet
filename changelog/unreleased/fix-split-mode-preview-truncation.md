---
type: fixed
---
Right side of Claude's output getting chopped off in split-view preview. tmux sessions are now sized to match the preview pane width (new sessions boot at that size, existing sessions resize on startup and whenever the terminal resizes), so Claude wraps to fit instead of rendering wider than fleet can display and being truncated by the ANSI cutter. Attaching temporarily resizes tmux up to the host terminal so the full-screen view isn't cramped; detaching restores the preview-fit size.
