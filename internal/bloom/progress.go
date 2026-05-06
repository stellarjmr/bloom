package bloom

import (
	"fmt"
	"io"
	"strings"
)

const (
	colorGreen = "\033[38;5;2m"
	colorGray  = "\033[38;5;238m"
	colorRed   = "\033[0;31m"
	colorCyan  = "\033[0;36m"
	colorReset = "\033[0m"
)

type Progress struct {
	out io.Writer
	cfg Config
}

func NewProgress(out io.Writer, cfg Config) Progress {
	return Progress{out: out, cfg: cfg}
}

func (p Progress) Render(done, total int, result TaskResult) {
	marker := "[ok]"
	color := colorGreen
	if result.Status == StatusSkipped {
		marker = "[skip]"
		color = colorGray
	}
	if result.Status == StatusDryRun {
		marker = "[dry]"
		color = colorCyan
	}
	if result.Err != nil {
		marker = "[fail]"
		color = colorRed
	}

	if !p.cfg.Color {
		color = ""
	}
	reset := ""
	if p.cfg.Color {
		reset = colorReset
	}

	message := result.Message
	if message != "" {
		message = " " + message
	}
	fmt.Fprintf(p.out, "%s %s%s%s %s%s\n", p.Bar(done, total), color, marker, reset, result.Name, message)
}

func (p Progress) Bar(done, total int) string {
	width := p.cfg.ProgressWidth
	if width <= 0 {
		width = 24
	}
	if total <= 0 {
		total = 1
	}
	filled := done * width / total
	if filled > width {
		filled = width
	}
	empty := width - filled
	percent := done * 100 / total

	filledBar := strings.Repeat("━", filled)
	emptyBar := strings.Repeat("─", empty)
	if filled > 0 && filled < width {
		filledBar = filledBar[:len(filledBar)-len("━")] + "╸"
	}

	if !p.cfg.Color {
		return fmt.Sprintf("[%s%s] %3d%%", filledBar, emptyBar, percent)
	}
	return fmt.Sprintf("[%s%s%s%s%s] %3d%%", colorGreen, filledBar, colorReset, colorGray, emptyBar+colorReset, percent)
}
