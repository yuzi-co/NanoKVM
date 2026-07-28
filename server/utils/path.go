package utils

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// safeName matches names that are safe both as a path component and as an
// argument to a shell command: letters, digits, dot, dash and underscore only,
// and never starting with a dot or a dash.
var safeName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

var errUnsafeName = errors.New("unsafe file name")

// IsSafeFileName reports whether name is a plain file name: no directory
// components, no traversal, and nothing a shell would treat specially.
func IsSafeFileName(name string) bool {
	if !safeName.MatchString(name) {
		return false
	}

	return name == filepath.Base(name)
}

// SecureJoin joins a caller-supplied file name onto a directory, rejecting
// anything that is not a plain name inside that directory.
func SecureJoin(dir string, name string) (string, error) {
	if !IsSafeFileName(name) {
		return "", errUnsafeName
	}

	return filepath.Join(dir, name), nil
}

// IsPathInside reports whether path is an absolute path to something strictly
// below dir, after resolving any "." and ".." segments.
func IsPathInside(dir string, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}

	cleaned := filepath.Clean(path)
	prefix := filepath.Clean(dir) + string(filepath.Separator)

	return strings.HasPrefix(cleaned, prefix)
}
