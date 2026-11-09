package gogio

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// dexInput packs d8's inputs - loose .class files, under root, and jars
// - into one jar at out, so d8's command line stays short however many
// libraries an app has: Windows limits a command line to 32767
// characters, and runs d8.bat through cmd.exe, which takes 8191.
//
// Only classes go in, as d8 reads only those; a class defined twice is
// an error, as it would be in d8.
func dexInput(files []string, root, out string) error {
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	from := map[string]string{}
	add := func(name, src string) (bool, error) {
		if prev, ok := from[name]; ok {
			if prev == src {
				return false, nil
			}
			return false, fmt.Errorf("class %s is defined in both %s and %s", strings.TrimSuffix(name, ".class"), prev, src)
		}
		from[name] = src
		return true, nil
	}
	err = func() error {
		for _, p := range files {
			if strings.HasSuffix(p, ".class") {
				name := looseClassName(p, root)
				if ok, err := add(name, p); err != nil || !ok {
					return err
				}
				if err := copyFileInto(w, name, p); err != nil {
					return err
				}
				continue
			}
			if err := copyJarClasses(w, p, add); err != nil {
				return err
			}
		}
		return nil
	}()
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// looseClassName is a class file's entry name: its path under root
// without the first directory (the javac output dir), "org/gioui/Gio.class".
func looseClassName(p, root string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(p)
	}
	rel = filepath.ToSlash(rel)
	if i := strings.IndexByte(rel, '/'); i >= 0 {
		rel = rel[i+1:]
	}
	return rel
}

func copyFileInto(w *zip.Writer, name, src string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	dst, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, r)
	return err
}

// copyJarClasses copies a jar's classes, as stored, leaving out
// resources, META-INF (signatures, other Java versions' classes) and
// module-info.
func copyJarClasses(w *zip.Writer, jar string, add func(name, src string) (bool, error)) error {
	r, err := zip.OpenReader(jar)
	if err != nil {
		return fmt.Errorf("%s: %w", jar, err)
	}
	defer r.Close()
	files := append([]*zip.File(nil), r.File...)
	sort.SliceStable(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	for _, zf := range files {
		name := zf.Name
		if !strings.HasSuffix(name, ".class") || strings.HasPrefix(name, "META-INF/") || strings.HasSuffix(name, "module-info.class") {
			continue
		}
		ok, err := add(name, jar)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if err := w.Copy(zf); err != nil {
			return fmt.Errorf("%s: %w", jar, err)
		}
	}
	return nil
}

// argFile writes args to a file javac reads with @file, one per line,
// quoted, so a long list of sources doesn't make the command line too
// long for Windows.
func argFile(path string, args []string) (string, error) {
	var b strings.Builder
	for _, a := range args {
		a = strings.ReplaceAll(a, `\`, `\\`)
		a = strings.ReplaceAll(a, `"`, `\"`)
		b.WriteString(`"` + a + "\"\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return "@" + path, nil
}
