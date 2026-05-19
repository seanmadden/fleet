package ui

import (
	"time"
)

// RenderStats accumulates rendering-related events for diagnostics.
type RenderStats struct {
	// Window resize tracking.
	ResizeCount    int       // total WindowSizeMsg events received
	LastResizeTime time.Time // when the last resize happened
	LastResizeW    int       // last resize width
	LastResizeH    int       // last resize height

	// Viewport drift tracking.
	ViewportDriftCount int // times viewOffset changed in syncViewport

	// Height mismatch tracking.
	HeightMismatchCount int // total View() calls where output lines != h.height
	LastMismatchDiff    int // last observed diff (output_lines - expected)
}

// RecordResize records a WindowSizeMsg event.
func (rs *RenderStats) RecordResize(w, h int) {
	rs.ResizeCount++
	rs.LastResizeTime = time.Now()
	rs.LastResizeW = w
	rs.LastResizeH = h
}

// RecordViewportDrift records a viewOffset change in syncViewport.
func (rs *RenderStats) RecordViewportDrift() {
	rs.ViewportDriftCount++
}

// RecordHeightMismatch records a View() height mismatch.
func (rs *RenderStats) RecordHeightMismatch(diff int) {
	rs.HeightMismatchCount++
	rs.LastMismatchDiff = diff
}
