package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestBoltFramesKeepTheirSize(t *testing.T) {
	for frame := range boltFrames {
		lines := bolt(frame)
		if len(lines) != len(boltRows) {
			t.Fatalf("frame %d has %d lines, want %d", frame, len(lines), len(boltRows))
		}
		for _, line := range lines {
			if w := lipgloss.Width(line); w != boltWidth {
				t.Errorf("frame %d line %q is %d columns, want %d", frame, line, w, boltWidth)
			}
		}
	}
}

func TestBoltSparksWithoutColour(t *testing.T) {
	// Tests run without a terminal, so lipgloss renders no colour, as with NO_COLOR.
	rest := strings.Join(bolt(0), "\n")
	if spark := strings.Join(bolt(4), "\n"); spark == rest || !strings.Contains(spark, "*") {
		t.Errorf("frame 4 should show a spark:\n%s", spark)
	}
	if again := strings.Join(bolt(boltFrames), "\n"); again != rest {
		t.Errorf("frames should loop:\n%s", again)
	}
}
