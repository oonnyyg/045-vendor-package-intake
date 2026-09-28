package packagegate

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// FileDecl is one data-file entry in manifest.json.
type FileDecl struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`

	sum []byte
}

// Manifest lists every data file delivered in the package. The manifest
// file itself is never listed.
type Manifest struct {
	Files []FileDecl `json:"files"`
}

const manifestName = "manifest.json"

func parseManifest(raw []byte) (*Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest.json invalid: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("manifest.json invalid: trailing data after JSON object")
	}
	if m.Files == nil {
		return nil, fmt.Errorf("manifest.json invalid: missing \"files\" array")
	}

	seenFolded := make(map[string]string, len(m.Files))
	for i := range m.Files {
		f := &m.Files[i]
		if err := validateRelPath(f.Path); err != nil {
			return nil, err
		}
		if f.Size < 0 {
			return nil, fmt.Errorf("manifest.json invalid: negative size for %s", f.Path)
		}
		sum, err := decodeHash(f.SHA256, f.Path)
		if err != nil {
			return nil, err
		}
		f.sum = sum

		folded := strings.ToLower(f.Path)
		if other, ok := seenFolded[folded]; ok {
			return nil, fmt.Errorf("manifest.json invalid: %s duplicates %s ignoring case", f.Path, other)
		}
		seenFolded[folded] = f.Path
	}
	if err := checkTreeConflicts(m.Files); err != nil {
		return nil, err
	}
	return &m, nil
}

func decodeHash(s, path string) ([]byte, error) {
	if len(s) != 64 {
		return nil, fmt.Errorf("manifest.json invalid: %s sha256 must be 64 hex characters", path)
	}
	sum, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("manifest.json invalid: %s sha256 not hex: %w", path, err)
	}
	return sum, nil
}

// checkTreeConflicts rejects a file path that is the same as, or a
// parent/child of, another declared path. Comparison ignores case.
func checkTreeConflicts(files []FileDecl) error {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = strings.ToLower(f.Path)
	}
	sort.Strings(paths)
	for i := 1; i < len(paths); i++ {
		prev, cur := paths[i-1], paths[i]
		if prev == cur || strings.HasPrefix(cur, prev+"/") {
			return fmt.Errorf("manifest.json invalid: file path conflict between %q and %q", prev, cur)
		}
	}
	return nil
}
