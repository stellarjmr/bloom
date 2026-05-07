package bloom

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	colorGreen = "\033[38;5;2m"
	colorGray  = "\033[38;5;238m"
	colorRed   = "\033[0;31m"
	colorCyan  = "\033[0;36m"
	colorReset = "\033[0m"
)

type Progress struct {
	out        io.Writer
	cfg        Config
	terminal   bool
	lineActive bool
	mu         sync.Mutex
}

func NewProgress(out io.Writer, cfg Config) *Progress {
	return &Progress{out: out, cfg: cfg, terminal: isTerminal(out)}
}

func (p *Progress) Render(done, total int, result TaskResult) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked(done, total, result, "")
}

func (p *Progress) renderLocked(done, total int, result TaskResult, markerOverride string) {
	marker := ""
	color := colorGreen
	if result.Status == StatusSkipped {
		marker = ""
		color = colorGray
	}
	if result.Status == StatusDryRun {
		marker = ""
		color = colorCyan
	}
	if result.Status == StatusRunning {
		marker = spinnerFrames[0]
		color = colorCyan
	}
	if markerOverride != "" {
		marker = markerOverride
	}
	if result.Err != nil {
		marker = ""
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
	line := fmt.Sprintf("%s %s%s%s %s%s", p.Bar(done, total), color, marker, reset, result.Name, message)
	if p.terminal {
		fmt.Fprintf(p.out, "\r\033[K%s", line)
		p.lineActive = true
		return
	}
	fmt.Fprintln(p.out, line)
}

func (p *Progress) RenderStart(done, total int, result TaskResult) {
	if !p.terminal {
		return
	}
	p.Render(done, total, result)
}

func (p *Progress) Animate(done, total int, result TaskResult) func() {
	if !p.terminal {
		return func() {}
	}
	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()

		frame := 0
		for {
			p.mu.Lock()
			p.renderLocked(done, total, result, spinnerFrames[frame])
			p.mu.Unlock()
			frame = (frame + 1) % len(spinnerFrames)

			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}

func (p *Progress) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.terminal && p.lineActive {
		fmt.Fprintln(p.out)
		p.lineActive = false
	}
}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (p *Progress) Bar(done, total int) string {
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

func isTerminal(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok || file == nil {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
