package progress

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableVT turns on the console's escape sequences (Windows 10 and
// later), which the live display needs.
func enableVT() bool {
	h := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
