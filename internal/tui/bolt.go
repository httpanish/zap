package tui

import "github.com/charmbracelet/lipgloss"

// boltRows is the mascot, drawn in half blocks. Every row is boltWidth columns.
var boltRows = [...]string{
	"   ▄█▀",
	" ▄██▄▄",
	"▀▀██▀ ",
	"▄█▀   ",
}

const (
	boltWidth  = 6
	boltFrames = 10 // One cycle a second at the 10 fps redraw rate.
)

var (
	boltStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffdd00"))
	flashStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffa0")).Bold(true)
	sparkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffee58"))
)

type spark struct {
	row, col int
	char     rune
}

// sparks are drawn in the empty cells by the tip, after the flash reaches it.
var sparks = map[int][]spark{
	4: {{3, 4, '*'}},
	5: {{3, 5, '·'}, {2, 5, '·'}},
}

// bolt renders one animation frame. A flash runs down the bolt one row per
// frame, then sparks jump off the tip, then the bolt rests. Sparks change the
// characters too, so the animation still shows when colour is off.
func bolt(frame int) []string {
	frame %= boltFrames
	lines := make([]string, len(boltRows))
	for row, text := range boltRows {
		cells := []rune(text)
		styles := make([]lipgloss.Style, len(cells))
		for col := range cells {
			styles[col] = boltStyle
			if row == frame {
				styles[col] = flashStyle
			}
		}
		for _, s := range sparks[frame] {
			if s.row == row {
				cells[s.col], styles[s.col] = s.char, sparkStyle
			}
		}
		for col, c := range cells {
			lines[row] += styles[col].Render(string(c))
		}
	}
	return lines
}
