package main

import (
	"errors"
	"reflect"
	"testing"
)

const hdiutilInfo = `framework       : 671.140.2
driver          : 10.13v671.140.2
================================================
image-path      : /Users/me/Downloads/Other.dmg
image-alias     : /Users/me/Downloads/Other.dmg
image-type      : read-only
/dev/disk4	GUID_partition_scheme	
/dev/disk4s1	Apple_HFS	/Volumes/Hello
================================================
image-path      : /private/var/folders/ab/T/hdiutil.x1/Hello.dmg
image-type      : read/write
/dev/disk5	GUID_partition_scheme	
/dev/disk5s1	Apple_HFS	/Volumes/Hello 1
================================================
image-path      : /private/var/folders/ab/T/other/Notes.dmg
/dev/disk6	GUID_partition_scheme	
/dev/disk6s1	Apple_HFS	/Volumes/Notes
================================================
image-path      : /build/Hello-1.0.0-arm64.dmg
/dev/disk7	GUID_partition_scheme	
/dev/disk7s1	Apple_HFS	/Volumes/Hello 2
`

// A failed hdiutil create's leftovers are the image itself and the
// temporary image mounted under the volume's name; another disk image
// of the same name the user opened, and other temporary images, stay.
func TestLeftoverDevices(t *testing.T) {
	got := leftoverDevices(hdiutilInfo, "Hello", "/build/Hello-1.0.0-arm64.dmg", []string{"/var/folders/ab/T", "/private/var/folders/ab/T/"})
	want := []string{"/dev/disk5", "/dev/disk7"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("leftovers %v, want %v", got, want)
	}
	if got := leftoverDevices("framework : 1\n", "Hello", "/x.dmg", nil); len(got) != 0 {
		t.Errorf("leftovers with nothing attached: %v", got)
	}
}

func TestRetry(t *testing.T) {
	busy := errors.New("hdiutil: create failed - Resource busy")
	tries, cleanups := 0, 0
	err := retry(5, 0, func() error {
		tries++
		if tries < 3 {
			return busy
		}
		return nil
	}, func(int, error) { cleanups++ })
	if err != nil || tries != 3 || cleanups != 2 {
		t.Errorf("succeeding on the third try: err %v, %d tries, %d cleanups", err, tries, cleanups)
	}

	tries, cleanups = 0, 0
	err = retry(4, 0, func() error { tries++; return busy }, func(int, error) { cleanups++ })
	if err != busy || tries != 4 || cleanups != 3 {
		t.Errorf("always failing: err %v, %d tries, %d cleanups; want the last error, 4 tries, 3 cleanups", err, tries, cleanups)
	}
}
