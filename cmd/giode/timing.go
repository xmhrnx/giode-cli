package main

import (
	"fmt"
	"time"
)

// timedCommands print how long they took when they finish. run isn't
// one: it lasts as long as the app does, so it reports its build and
// install steps instead.
var timedCommands = map[string]bool{
	"release": true, "upgrade": true, "self-update": true, "licenses": true, "clean": true,
}

// elapsed formats a duration for progress lines: 850ms, 12.3s, 2m05s.
func elapsed(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// since is elapsed(time.Since(start)).
func since(start time.Time) string {
	return elapsed(time.Since(start))
}

// after notes how long a failed step ran, when that's long enough to
// matter: " (after 1m02s)", or nothing.
func after(start time.Time) string {
	if d := time.Since(start); d >= time.Second {
		return " (after " + elapsed(d) + ")"
	}
	return ""
}
