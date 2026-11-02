package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

const progressBarWidth = 24

// progressUI renders a single changing status line on a terminal. When stderr
// is redirected it falls back to periodic plain-text lines. The zero value is
// disabled.
type progressUI struct {
	mu         sync.Mutex
	w          io.Writer
	enabled    bool
	tty        bool
	lineLen    int
	stage      string
	lastBucket int
	lastDone   int
	started    time.Time
}

func newProgressUI(w io.Writer, enabled bool) *progressUI {
	if w == nil {
		w = io.Discard
	}

	return &progressUI{
		w:          w,
		enabled:    enabled,
		tty:        isTerminal(w),
		lastBucket: -1,
	}
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// Status shows a stage that has no measurable progress yet.
func (u *progressUI) Status(message string) {
	if !u.enabled {
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if u.tty {
		u.clearLocked()
		fmt.Fprint(u.w, message)
		u.lineLen = len(message)

		return
	}

	fmt.Fprintln(u.w, message)
}

// Done ends the current stage with a completed status.
func (u *progressUI) Done(message string) {
	if !u.enabled {
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearLocked()

	fmt.Fprintf(u.w, "ok %s\n", message)

	u.lineLen = 0
	u.stage = ""
	u.lastBucket = -1
	u.lastDone = -1
}

// Update renders measurable progress. total == 0 means an indeterminate stage.
func (u *progressUI) Update(stage string, done, total int) {
	if !u.enabled {
		return
	}

	if total <= 0 {
		u.Status(stage)
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if u.stage != stage {
		u.clearLocked()
		u.stage = stage
		u.lastBucket = -1
		u.lastDone = -1
		u.started = time.Now()
	}

	if done < 0 {
		done = 0
	}

	if done > total {
		done = total
	}

	if done <= u.lastDone {
		return
	}

	u.lastDone = done

	if u.tty {
		pct := done * 100 / total
		elapsed := time.Since(u.started).Round(time.Second)
		line := fmt.Sprintf("%s %s %d/%d %3d%% %s", stage, renderBar(done, total), done, total, pct, elapsed)
		u.clearLocked()
		fmt.Fprint(u.w, line)
		u.lineLen = len(line)
		return
	}

	bucket := done * 10 / total

	if done != total && bucket == u.lastBucket {
		return
	}

	u.lastBucket = bucket
	elapsed := time.Since(u.started).Round(time.Second)
	fmt.Fprintf(u.w, "%s %d/%d (%d%%, %s)\n", stage, done, total, done*100/total, elapsed)
}

// Close clears an unfinished status line so an error message starts cleanly.
func (u *progressUI) Close() {
	if !u.enabled {
		return
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	if u.tty && u.lineLen > 0 {
		u.clearLocked()
		fmt.Fprintln(u.w)
	}

	u.lineLen = 0
}

func (u *progressUI) clearLocked() {
	if !u.tty || u.lineLen == 0 {
		return
	}

	fmt.Fprintf(u.w, "\r%s\r", strings.Repeat(" ", u.lineLen))
	u.lineLen = 0
}

func renderBar(done, total int) string {
	if total <= 0 {
		return strings.Repeat("-", progressBarWidth)
	}

	filled := done * progressBarWidth / total
	if filled < 0 {
		filled = 0
	}
	if filled > progressBarWidth {
		filled = progressBarWidth
	}

	return strings.Repeat("#", filled) + strings.Repeat("-", progressBarWidth-filled)
}

func pluralize(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}

	return fmt.Sprintf("%d %s", n, plural)
}
