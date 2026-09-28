package gate

import (
	"regexp"
	"strings"
)

// validateRelPath enforces the portable, sandbox-safe relative path rules
// required by packagegate. It returns the slash-separated path and its
// lowercase segments used for case-insensitive collision checks.
func validateRelPath(p string) (string, []string, error) {
	if p == "" {
		return "", nil, errBadPath("empty path")
	}
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == '/':
		default:
			return "", nil, errBadPath("path contains disallowed character: " + p)
		}
	}
	segments := strings.Split(p, "/")
	lower := make([]string, len(segments))
	for i, seg := range segments {
		if seg == "" {
			return "", nil, errBadPath("empty path segment: " + p)
		}
		if seg == "." || seg == ".." {
			return "", nil, errBadPath("dot path segment not allowed: " + p)
		}
		if strings.HasSuffix(seg, ".") {
			return "", nil, errBadPath("trailing dot in file name: " + p)
		}
		if isReservedName(seg) {
			return "", nil, errBadPath("reserved file name: " + p)
		}
		lower[i] = strings.ToLower(seg)
	}
	return p, lower, nil
}

var reservedBase = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])($|\.)`)

// isReservedName reports Windows-reserved device names such as CON or NUL,
// including the name carrying a trailing extension (e.g. "NUL.txt").
func isReservedName(seg string) bool {
	return reservedBase.MatchString(strings.ToLower(seg))
}
