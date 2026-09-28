package packagegate

import (
	"strings"
)

// validateRelPath checks a manifest-relative data file path.
// Allowed characters: ASCII letters, digits, '.', '_', '-', '/'.
// Rejected: absolute paths, backslashes, colons, empty segments, "."/".."
// segments, and file names that are illegal on Windows.
func validateRelPath(p string) error {
	if p == "" {
		return &PathError{Path: p, Reason: "empty path"}
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return &PathError{Path: p, Reason: "absolute path not allowed"}
	}
	for _, r := range p {
		if !allowedRune(r) {
			return &PathError{Path: p, Reason: "character not allowed: " + string(r)}
		}
	}

	segments := strings.Split(p, "/")
	for _, seg := range segments {
		if seg == "" {
			return &PathError{Path: p, Reason: "empty path segment"}
		}
		if seg == "." || seg == ".." {
			return &PathError{Path: p, Reason: "dot path segment not allowed"}
		}
		if isIllegalName(seg) {
			return &PathError{Path: p, Reason: "illegal file name: " + seg}
		}
	}
	return nil
}

func allowedRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '.' || r == '_' || r == '-' || r == '/':
		return true
	default:
		return false
	}
}

// isIllegalName reports names Windows refuses to create: reserved device
// names and names ending in a dot or space.
func isIllegalName(seg string) bool {
	name := seg
	if dot := strings.IndexByte(seg, '.'); dot >= 0 {
		name = seg[:dot]
	}
	switch strings.ToUpper(name) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
		return true
	}
	return false
}

// PathError describes a rejected relative path.
type PathError struct {
	Path   string
	Reason string
}

func (e *PathError) Error() string {
	return "invalid path " + e.Path + ": " + e.Reason
}
