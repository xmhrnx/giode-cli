// Package progress shows what giode build is doing as it happens: each
// target, the steps running under it (with a spinner and their time,
// several at once when they run in parallel), and a summary at the end.
//
// On a terminal the running steps redraw in place; elsewhere (CI logs,
// a pipe, NO_COLOR, -x) each step prints a plain line when it ends.
//
// Code anywhere in the build reports through the package functions -
// Begin, Command, Logf - which do nothing while no Reporter is running,
// so the same code serves giode run and the other commands quietly.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/term"
)

var current atomic.Pointer[Reporter]

// Reporter draws a build's progress.
type Reporter struct {
	w     io.Writer
	live  bool // redraw running steps in place
	color bool
	width func() int

	mu      sync.Mutex
	active  []*step
	drawn   int // lines of the live block on screen
	frame   int
	results []result
	target  string
	tStart  time.Time
	start   time.Time
	stop    chan struct{}
	done    chan struct{}
}

type step struct {
	label string
	start time.Time
}

type result struct {
	target, out string
	size        int64
	took        time.Duration
	err         error
}

// Options configure a Reporter.
type Options struct {
	// Plain prints a line per finished step instead of redrawing;
	// set with -x, whose command lines would break the live display.
	Plain bool
}

// Start begins reporting to standard output, live on a terminal, and
// makes the Reporter the one the package functions use.
func Start(o Options) *Reporter {
	r := &Reporter{w: os.Stdout, start: time.Now(), width: func() int { return 80 }}
	fd := int(os.Stdout.Fd())
	if term.IsTerminal(fd) && os.Getenv("TERM") != "dumb" && enableVT() {
		r.color = os.Getenv("NO_COLOR") == ""
		r.live = !o.Plain
		r.width = func() int {
			if w, _, err := term.GetSize(fd); err == nil && w > 20 {
				return w
			}
			return 80
		}
	}
	if r.live {
		r.stop, r.done = make(chan struct{}), make(chan struct{})
		go r.loop()
	}
	current.Store(r)
	return r
}

// Stop ends live drawing; Summary prints the results.
func (r *Reporter) Stop() {
	current.CompareAndSwap(r, nil)
	if r.live {
		close(r.stop)
		<-r.done
		r.mu.Lock()
		r.clear()
		r.mu.Unlock()
	}
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (r *Reporter) loop() {
	defer close(r.done)
	t := time.NewTicker(90 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			r.mu.Lock()
			r.frame++
			r.redraw()
			r.mu.Unlock()
		}
	}
}

// clear erases the live block. r.mu is held.
func (r *Reporter) clear() {
	if r.drawn == 0 {
		return
	}
	fmt.Fprintf(r.w, "\x1b[%dA\r\x1b[J", r.drawn)
	r.drawn = 0
}

// redraw replaces the live block with the running steps. r.mu is held.
func (r *Reporter) redraw() {
	if !r.live {
		return
	}
	var b strings.Builder
	if r.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA\r\x1b[J", r.drawn)
	}
	width := r.width()
	n := 0
	for _, s := range r.active {
		spin := r.paint(cyan, spinner[r.frame%len(spinner)])
		b.WriteString(r.row(width, "    "+spin+" "+s.label, time.Since(s.start), dim))
		b.WriteByte('\n')
		n++
	}
	if r.target != "" {
		b.WriteString(r.paint(dim, fit(fmt.Sprintf("    building %s · %s", r.target, duration(time.Since(r.tStart))), width-1)))
		b.WriteByte('\n')
		n++
	}
	r.drawn = n
	io.WriteString(r.w, b.String())
}

// println prints a permanent line above the live block.
func (r *Reporter) println(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.live {
		r.clear()
	}
	fmt.Fprintln(r.w, s)
	if r.live {
		r.redraw()
	}
}

// row is "text ........ 1.2s", fitted to the terminal.
func (r *Reporter) row(width int, text string, d time.Duration, c string) string {
	t := duration(d)
	pad := 46 - visibleLen(text)
	if pad < 2 {
		pad = 2
	}
	if visibleLen(text)+pad+len(t) >= width {
		return text
	}
	return text + strings.Repeat(" ", pad) + r.paint(c, t)
}

// Header prints the build's first lines: the app and its targets.
func (r *Reporter) Header(app, version string, targets []string) {
	r.println("")
	r.println("  " + r.paint(bold, "giode build") + "  " + app + " " + r.paint(dim, version) +
		r.paint(dim, "  ·  ") + strings.Join(targets, ", "))
}

// Target starts the i'th of n targets.
func (r *Reporter) Target(name string, i, n int) {
	r.mu.Lock()
	r.target, r.tStart = name, time.Now()
	r.mu.Unlock()
	head := "  " + r.paint(bold+cyan, "▸ "+name)
	if n > 1 {
		head += r.paint(dim, fmt.Sprintf("  %d/%d", i, n))
	}
	r.println("")
	r.println(head)
}

// TargetDone ends the current target with its output (a file or
// folder) or error.
func (r *Reporter) TargetDone(out string, err error) {
	r.mu.Lock()
	res := result{target: r.target, out: out, took: time.Since(r.tStart), err: err}
	r.target = ""
	r.mu.Unlock()
	if err == nil {
		res.size = size(out)
	}
	r.mu.Lock()
	r.results = append(r.results, res)
	r.mu.Unlock()
	if err != nil {
		r.println("  " + r.paint(red, "✗ "+res.target) + r.paint(dim, " failed after "+duration(res.took)))
		for _, l := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
			r.println("    " + r.paint(red, l))
		}
		return
	}
	line := "  " + r.paint(green, "✓ "+res.target) + "  " + out
	if res.size > 0 {
		line += r.paint(dim, " · "+byteSize(res.size))
	}
	r.println(line + r.paint(dim, " · "+duration(res.took)))
}

// Summary prints every target's result and the total time.
func (r *Reporter) Summary() {
	r.mu.Lock()
	results := append([]result(nil), r.results...)
	r.mu.Unlock()
	if len(results) == 0 {
		return
	}
	failed := 0
	for _, res := range results {
		if res.err != nil {
			failed++
		}
	}
	r.println("")
	head := fmt.Sprintf("Done in %s", duration(time.Since(r.start)))
	switch {
	case failed == len(results):
		head = r.paint(red+bold, fmt.Sprintf("Failed after %s", duration(time.Since(r.start))))
	case failed > 0:
		head = r.paint(bold, head) + r.paint(dim, fmt.Sprintf(" · %d built, ", len(results)-failed)) + r.paint(red, fmt.Sprintf("%d failed", failed))
	default:
		head = r.paint(green+bold, head) + r.paint(dim, fmt.Sprintf(" · %d built", len(results)))
	}
	r.println("  " + head)
	if len(results) < 2 {
		r.println("")
		return
	}
	nameW := 0
	for _, res := range results {
		nameW = max(nameW, len(res.target))
	}
	for _, res := range results {
		name := res.target + strings.Repeat(" ", nameW-len(res.target))
		if res.err != nil {
			r.println("    " + r.paint(red, "✗ "+name) + "  " + r.paint(dim, "failed"))
			continue
		}
		line := "    " + r.paint(green, "✓ ") + name + "  " + res.out
		if res.size > 0 {
			line += r.paint(dim, "  "+byteSize(res.size))
		}
		r.println(line)
	}
	r.println("")
}

// begin starts a step and returns the function ending it.
func (r *Reporter) begin(label string) func(error) {
	s := &step{label: label, start: time.Now()}
	r.mu.Lock()
	r.active = append(r.active, s)
	r.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() { r.end(s, err) })
	}
}

func (r *Reporter) end(s *step, err error) {
	r.mu.Lock()
	for i, a := range r.active {
		if a == s {
			r.active = append(r.active[:i], r.active[i+1:]...)
			break
		}
	}
	r.mu.Unlock()
	took := time.Since(s.start)
	if err == nil && took < 100*time.Millisecond {
		return // nothing worth a line
	}
	mark, c := r.paint(green, "✓"), dim
	if err != nil {
		mark, c = r.paint(red, "✗"), red
	}
	r.println(r.row(r.width(), "    "+mark+" "+s.label, took, c))
}

// Begin starts a step of the running build, labelled for people
// ("Patching Gio"), and returns the function that ends it, with the
// step's error or nil. Without a running Reporter it does nothing.
func Begin(label string) func(error) {
	r := current.Load()
	if r == nil {
		return func(error) {}
	}
	return r.begin(label)
}

// Logf prints a note (a warning, where a file went) under the current
// target, or to standard error without a running Reporter.
func Logf(format string, args ...any) {
	msg := strings.TrimSpace(fmt.Sprintf(format, args...))
	r := current.Load()
	if r == nil {
		fmt.Fprintln(os.Stderr, "  "+msg)
		return
	}
	for _, l := range strings.Split(msg, "\n") {
		r.println("    " + r.paint(yellow, "· ") + l)
	}
}

// Running reports whether a Reporter is showing the build's progress.
func Running() bool { return current.Load() != nil }

// Live reports whether a Reporter is redrawing the terminal, so
// commands' own output must be captured rather than shown.
func Live() bool {
	r := current.Load()
	return r != nil && r.live
}

const (
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	green  = "\x1b[32m"
	yellow = "\x1b[33m"
	cyan   = "\x1b[36m"
	reset  = "\x1b[0m"
)

func (r *Reporter) paint(c, s string) string {
	if !r.color || s == "" {
		return s
	}
	return c + s + reset
}

// visibleLen is the length of s without colour codes.
func visibleLen(s string) int {
	n, esc := 0, false
	for _, c := range s {
		switch {
		case c == '\x1b':
			esc = true
		case esc:
			if c == 'm' {
				esc = false
			}
		default:
			n++
		}
	}
	return n
}

// fit cuts plain text to width runes.
func fit(s string, width int) string {
	if width < 1 || utf8.RuneCountInString(s) <= width {
		return s
	}
	return string([]rune(s)[:width-1]) + "…"
}

// duration is a short, human time: 0.4s, 12.3s, 2m05s.
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// byteSize is a short, human size.
func byteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// size is a file's size or a folder's total, 0 when unknown.
func size(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !st.IsDir() {
		return st.Size()
	}
	var n int64
	walk(path, &n)
	return n
}

func walk(dir string, n *int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		p := dir + string(os.PathSeparator) + e.Name()
		if e.IsDir() {
			walk(p, n)
		} else if info, err := e.Info(); err == nil {
			*n += info.Size()
		}
	}
}
