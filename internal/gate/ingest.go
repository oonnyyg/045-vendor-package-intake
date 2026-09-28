package gate

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ingest verifies the vendor ZIP package and, only when every check passes,
// publishes its data files into opts.TargetDir. On any failure the target is
// left untouched and all temporary data is removed.
func Ingest(opts Options) (*Receipt, error) {
	if opts.MaxEntries <= 0 {
		return nil, fail("max entries must be a positive integer")
	}
	if opts.MaxTotalBytes <= 0 {
		return nil, fail("max total bytes must be a positive integer")
	}

	reader, err := zip.OpenReader(opts.ZipPath)
	if err != nil {
		return nil, fail("cannot open zip package: %v", err)
	}
	defer reader.Close()

	if len(reader.File) > opts.MaxEntries {
		return nil, fail("zip contains %d entries, limit is %d", len(reader.File), opts.MaxEntries)
	}

	// The final destination must not exist yet, while its parent must.
	targetAbs, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		return nil, fail("cannot resolve target path: %v", err)
	}
	parent := filepath.Dir(targetAbs)
	if info, statErr := os.Stat(parent); statErr != nil || !info.IsDir() {
		return nil, fail("target parent directory does not exist: %s", parent)
	}
	if _, err := os.Stat(targetAbs); !os.IsNotExist(err) {
		if err == nil {
			return nil, fail("target already exists: %s", targetAbs)
		}
		return nil, fail("cannot inspect target: %v", err)
	}

	var manifestFileHeader *zip.File
	dataEntries := make(map[string]*zip.File, len(reader.File))
	for _, f := range reader.File {
		if strings.HasSuffix(f.Name, "/") {
			return nil, fail("directory entries are not allowed: %s", f.Name)
		}
		if f.Mode()&os.ModeType != 0 {
			return nil, fail("only regular file entries are allowed: %s", f.Name)
		}
		switch f.Name {
		case manifestName:
			if manifestFileHeader != nil {
				return nil, fail("duplicate manifest entry")
			}
			manifestFileHeader = f
		default:
			dataEntries[f.Name] = f
		}
	}
	if manifestFileHeader == nil {
		return nil, fail("package root must contain manifest.json")
	}

	var total int64
	manifestBytes, err := readZipEntry(manifestFileHeader, &total, opts.MaxTotalBytes)
	if err != nil {
		return nil, err
	}

	declared, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, err
	}

	declaredSet := make(map[string]manifestEntry, len(declared))
	declaredLower := make(map[string][]string, len(declared))
	for _, entry := range declared {
		clean, lower, err := validateRelPath(entry.Path)
		if err != nil {
			return nil, err
		}
		if entry.Length < 0 {
			return nil, fail("manifest declares negative length for %s", clean)
		}
		if len(entry.SHA256) != 64 {
			return nil, fail("manifest sha256 for %s must be 64 hex characters", clean)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return nil, fail("manifest sha256 for %s is not valid hex", clean)
		}
		if _, dup := declaredSet[clean]; dup {
			return nil, fail("manifest declares duplicate path: %s", clean)
		}
		declaredSet[clean] = entry
		declaredLower[clean] = lower
	}

	// Exact set match: no missing, extra or duplicate zip entries.
	for path := range declaredSet {
		if _, ok := dataEntries[path]; !ok {
			return nil, fail("declared file missing from package: %s", path)
		}
	}
	for path := range dataEntries {
		if _, ok := declaredSet[path]; !ok {
			return nil, fail("package entry not listed in manifest: %s", path)
		}
		if _, _, err := validateRelPath(path); err != nil {
			return nil, err
		}
	}
	if len(declaredSet) != len(dataEntries) {
		return nil, fail("manifest lists %d data files but package contains %d entries", len(declaredSet), len(dataEntries))
	}

	// Case-insensitive duplicates and file/subtree conflicts (e.g. "a"
	// alongside "a/b.txt") are rejected regardless of declared ordering.
	paths := make([]string, 0, len(declaredSet))
	for path := range declaredSet {
		paths = append(paths, path)
	}
	if err := checkCollisions(paths, declaredLower); err != nil {
		return nil, err
	}
	sort.Strings(paths)

	results := make([]FileResult, 0, len(paths))
	tmpDir, err := os.MkdirTemp(parent, ".packagegate-*")
	if err != nil {
		return nil, fail("cannot create staging directory: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			os.RemoveAll(tmpDir)
		}
	}()

	for _, path := range paths {
		entry := declaredSet[path]
		content, err := readZipEntry(dataEntries[path], &total, opts.MaxTotalBytes)
		if err != nil {
			return nil, err
		}
		if int64(len(content)) != entry.Length {
			return nil, fail("length mismatch for %s: got %d, manifest says %d", path, len(content), entry.Length)
		}
		sum := sha256.Sum256(content)
		actualHex := hex.EncodeToString(sum[:])
		if !strings.EqualFold(actualHex, entry.SHA256) {
			return nil, fail("sha256 mismatch for %s: got %s", path, actualHex)
		}

		dest := filepath.Join(tmpDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fail("cannot create directory for %s: %v", path, err)
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return nil, fail("cannot write staged file %s: %v", path, err)
		}
		results = append(results, FileResult{Path: path, Length: int64(len(content)), SHA256: actualHex})
	}

	// Final guard before the rename, closing the check-then-act window.
	if _, err := os.Stat(targetAbs); !os.IsNotExist(err) {
		return nil, fail("target already exists: %s", targetAbs)
	}
	if err := os.Rename(tmpDir, targetAbs); err != nil {
		return nil, fail("cannot publish target directory: %v", err)
	}
	committed = true

	return &Receipt{
		Target:     targetAbs,
		FileCount:  len(results),
		TotalBytes: total - int64(len(manifestBytes)),
		Files:      results,
	}, nil
}

func readZipEntry(f *zip.File, total *int64, ceiling int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fail("cannot open zip entry %s: %v", f.Name, err)
	}
	defer rc.Close()
	lr := &limitedReader{r: rc, total: total, remaining: ceiling - *total}
	data, err := io.ReadAll(lr)
	if err != nil {
		if ge, ok := err.(*GateError); ok {
			return nil, ge
		}
		return nil, fail("corrupt zip entry %s: %v", f.Name, err)
	}
	return data, nil
}

func parseManifest(data []byte) ([]manifestEntry, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	var mf manifestFile
	if err := dec.Decode(&mf); err != nil {
		return nil, fail("invalid manifest.json: %v", err)
	}
	if mf.Files == nil {
		return nil, fail("invalid manifest.json: missing \"files\" array")
	}
	return mf.Files, nil
}

// checkCollisions rejects case-insensitive name clashes and paths where a
// file occupies a directory segment of another path.
func checkCollisions(paths []string, lower map[string][]string) error {
	fileByLowerPath := make(map[string]string)
	dirSegments := make(map[string]bool)
	for _, p := range paths {
		segs := lower[p]
		key := strings.Join(segs, "/")
		if other, ok := fileByLowerPath[key]; ok {
			return fail("paths differ only in case: %s vs %s", other, p)
		}
		fileByLowerPath[key] = p
		for i := 0; i < len(segs)-1; i++ {
			dirSegments[strings.Join(segs[:i+1], "/")] = true
		}
	}
	for key, owner := range fileByLowerPath {
		if dirSegments[key] {
			return fail("file path conflicts with a subdirectory: %s", owner)
		}
	}
	return nil
}
