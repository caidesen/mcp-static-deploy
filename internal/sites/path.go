package sites

import (
	"errors"
	"path"
	"strings"
)

const maxRelPath = 512

// ErrBadPath means the caller tried to write or delete outside the site root.
var ErrBadPath = errors.New("invalid path")

// CleanRelPath returns a slash-separated path relative to a site root.
// Absolute paths, drive paths, and any path that escapes the root are rejected.
func CleanRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) {
		return "", ErrBadPath
	}
	if len(p) > maxRelPath {
		return "", ErrBadPath
	}
	if filepathIsAbs(p) {
		return "", ErrBadPath
	}
	p = strings.ReplaceAll(p, `\`, "/")
	if strings.HasPrefix(p, "/") {
		return "", ErrBadPath
	}
	// "C:foo" or "C:/foo" after the slash rewrite.
	if len(p) >= 2 && p[1] == ':' {
		return "", ErrBadPath
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", ErrBadPath
	}
	if len(cleaned) > maxRelPath {
		return "", ErrBadPath
	}
	return cleaned, nil
}

// filepathIsAbs reports absolute paths without importing path/filepath rules
// that treat a leading slash only. A Windows-style drive prefix is absolute too.
func filepathIsAbs(p string) bool {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return true
	}
	if len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') {
		return true
	}
	return false
}
