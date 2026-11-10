package main

import (
	"bufio"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/xmhrnx/giode-cli/internal/progress"
)

// dmgAttempts and dmgPause pace the retries of hdiutil create.
var (
	dmgAttempts = 5
	dmgPause    = 5 * time.Second
)

// createDMG makes a compressed disk image of stage at out, named
// volname. hdiutil create fails now and then, on GitHub's macOS runners
// most often with "Resource busy" while XProtect scans the new image,
// so it's tried again, after detaching any volume the failed attempt
// left attached and removing a partial image.
func createDMG(volname, stage, out string) error {
	size, err := dirSize(stage)
	if err != nil {
		return err
	}
	return retry(dmgAttempts, dmgPause, func() error {
		return run(exec.Command("hdiutil", "create", "-volname", volname, "-srcfolder", stage, "-ov",
			"-fs", "HFS+", "-size", dmgSize(size), "-format", "UDZO", out))
	}, func(attempt int, err error) {
		progress.Logf("hdiutil create failed (attempt %d of %d), trying again in %s:\n%v", attempt, dmgAttempts, dmgPause, err)
		detachLeftovers(volname, out)
		os.Remove(out)
	})
}

// dmgSize is the size of the image hdiutil fills from the app, of size
// bytes, in megabytes: with its own estimate, hdiutil can run out of
// room in the image ("No space left on device"), so it gets the app's
// size and a margin for the file system.
func dmgSize(size int64) string {
	const mb = 1 << 20
	return strconv.FormatInt((size+size/4+32*mb+mb-1)/mb, 10) + "m"
}

// dirSize adds up the sizes of the files under dir, not following links
// (the stage's link to /Applications).
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// retry calls try up to attempts times until it succeeds, calling
// between and waiting pause after each failure but the last, and
// returns the last error.
func retry(attempts int, pause time.Duration, try func() error, between func(attempt int, err error)) error {
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err = try(); err == nil {
			return nil
		}
		if attempt < attempts {
			between(attempt, err)
			time.Sleep(pause)
		}
	}
	return err
}

// detachLeftovers force-detaches the disk images a failed hdiutil create
// left attached: out itself, or a temporary image mounted under the
// volume's name.
func detachLeftovers(volname, out string) {
	info, err := exec.Command("hdiutil", "info").Output()
	if err != nil {
		return
	}
	for _, dev := range leftoverDevices(string(info), volname, out, tempDirs()) {
		if msg, err := exec.Command("hdiutil", "detach", dev, "-force").CombinedOutput(); err != nil {
			progress.Logf("hdiutil detach %s: %v %s", dev, err, strings.TrimSpace(string(msg)))
		}
	}
}

// tempDirs are where hdiutil puts the temporary image -srcfolder fills:
// the temporary directory, as given and with symlinks resolved
// (/var/folders is /private/var/folders).
func tempDirs() []string {
	dirs := []string{os.TempDir()}
	if real, err := filepath.EvalSymlinks(os.TempDir()); err == nil && real != dirs[0] {
		dirs = append(dirs, real)
	}
	return dirs
}

// leftoverDevices reads hdiutil info and returns the whole-disk device
// of each image that is out, or that lies in one of temps and is mounted
// at /Volumes/<volname> (or /Volumes/<volname> 1, ... when the name was
// taken).
func leftoverDevices(info, volname, out string, temps []string) []string {
	var devs []string
	var image, dev string
	mounted := false
	flush := func() {
		if dev != "" && image != "" && (image == out || mounted && inAny(image, temps)) {
			devs = append(devs, dev)
		}
		image, dev, mounted = "", "", false
	}
	sc := bufio.NewScanner(strings.NewReader(info))
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "====="):
			flush()
		case strings.HasPrefix(line, "image-path"):
			if _, v, ok := strings.Cut(line, ":"); ok {
				image = strings.TrimSpace(v)
			}
		case strings.HasPrefix(line, "/dev/disk"):
			fields := strings.Split(line, "\t")
			if dev == "" {
				dev = strings.TrimSpace(fields[0])
			}
			if mount := strings.TrimSpace(fields[len(fields)-1]); mount == "/Volumes/"+volname || strings.HasPrefix(mount, "/Volumes/"+volname+" ") {
				mounted = true
			}
		}
	}
	flush()
	return devs
}

// inAny reports whether path lies under one of dirs.
func inAny(path string, dirs []string) bool {
	for _, d := range dirs {
		if d != "" && strings.HasPrefix(path, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}
