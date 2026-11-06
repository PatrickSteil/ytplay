package ui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// expandHome turns a leading ~ into the home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// completePath completes the path being typed. It returns the longest
// unambiguous completion and all candidates (directories end in "/").
func completePath(typed string) (string, []string) {
	dirPart, base := "", typed
	if i := strings.LastIndex(typed, "/"); i >= 0 {
		dirPart, base = typed[:i+1], typed[i+1:]
	}
	real := expandHome(dirPart)
	if dirPart == "" {
		real = "."
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return typed, nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, base) || (strings.HasPrefix(n, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		if e.IsDir() {
			n += "/"
		} else if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(real, n)); err == nil && st.IsDir() {
				n += "/"
			}
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return typed, nil
	}
	sort.Strings(names)
	common := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, common) {
			common = common[:len(common)-1]
		}
	}
	return dirPart + common, names
}

// defaultDownloadPath suggests ~/Music/<title>.mp3.
func defaultDownloadPath(title string) string {
	var b strings.Builder
	sep := true
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			sep = false
		} else if !sep {
			b.WriteByte('_')
			sep = true
		}
	}
	name := strings.Trim(b.String(), "_")
	if name == "" {
		name = "song"
	}
	return "~/Music/" + name + ".mp3"
}
