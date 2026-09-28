package packagegate

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Options controls the safety limits of an intake run.
type Options struct {
	MaxEntries int
	MaxBytes   int64
}

// FileResult is the verified outcome for one data file.
type FileResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Receipt is the JSON result returned on success.
type Receipt struct {
	Target    string       `json:"target"`
	FileCount int          `json:"file_count"`
	TotalSize int64        `json:"total_size"`
	Files     []FileResult `json:"files"`
}

// Intake validates the ZIP at zipPath and, only when every check passes,
// publishes its data files as a new directory at targetPath.
func Intake(zipPath, targetPath string, opts Options) (*Receipt, error) {
	if opts.MaxEntries <= 0 {
		return nil, fmt.Errorf("max entries must be a positive integer")
	}
	if opts.MaxBytes <= 0 {
		return nil, fmt.Errorf("max uncompressed bytes must be a positive integer")
	}

	parent := filepath.Dir(targetPath)
	switch info, err := os.Stat(parent); {
	case err != nil:
		return nil, fmt.Errorf("target parent directory not accessible: %w", err)
	case !info.IsDir():
		return nil, fmt.Errorf("target parent is not a directory: %s", parent)
	}
	if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
		if err == nil {
			return nil, fmt.Errorf("target path already exists: %s", targetPath)
		}
		return nil, fmt.Errorf("target path not accessible: %w", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open zip: %w", err)
	}
	defer zr.Close()

	entries, manifestRaw, used, err := collectEntries(&zr.Reader, opts)
	if err != nil {
		return nil, err
	}
	manifest, err := parseManifest(manifestRaw)
	if err != nil {
		return nil, err
	}
	if err := matchEntries(manifest, entries); err != nil {
		return nil, err
	}

	results, err := extractAll(&zr.Reader, manifest, opts, used)
	if err != nil {
		return nil, err
	}

	staging, err := os.MkdirTemp(parent, ".packagegate-stage-*")
	if err != nil {
		return nil, fmt.Errorf("cannot create staging directory: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(staging)
		}
	}()

	if err := publishFiles(staging, results); err != nil {
		return nil, err
	}
	if err := os.Rename(staging, targetPath); err != nil {
		return nil, fmt.Errorf("cannot publish target directory: %w", err)
	}
	cleanup = false

	receipt := buildReceipt(targetPath, results)
	return receipt, nil
}

type extractedFile struct {
	decl FileDecl
	sum  []byte
	size int64
	data []byte
}

// collectEntries performs all entry-level checks before any content is
// decompressed and returns the manifest bytes plus data entries keyed by
// their exact declared path.
func collectEntries(zr *zip.Reader, opts Options) (map[string]*zip.File, []byte, int64, error) {
	if len(zr.File) == 0 {
		return nil, nil, 0, fmt.Errorf("zip contains no entries")
	}
	if len(zr.File) > opts.MaxEntries {
		return nil, nil, 0, fmt.Errorf("entry count %d exceeds limit %d", len(zr.File), opts.MaxEntries)
	}

	entries := make(map[string]*zip.File, len(zr.File))
	folded := make(map[string]string, len(zr.File))
	var manifestEntry *zip.File

	for _, f := range zr.File {
		name := f.Name
		if strings.HasPrefix(name, "/") {
			return nil, nil, 0, &PathError{Path: name, Reason: "absolute path not allowed"}
		}
		mode := f.Mode()
		if mode.IsDir() {
			return nil, nil, 0, fmt.Errorf("zip entry is a directory: %s", name)
		}
		if mode&os.ModeSymlink != 0 {
			return nil, nil, 0, fmt.Errorf("zip entry is a symbolic link: %s", name)
		}
		if mode&os.ModeType != 0 {
			return nil, nil, 0, fmt.Errorf("zip entry is a special file: %s", name)
		}

		switch {
		case name == manifestName:
			if manifestEntry != nil {
				return nil, nil, 0, fmt.Errorf("duplicate manifest.json entry")
			}
			manifestEntry = f
		default:
			if err := validateRelPath(name); err != nil {
				return nil, nil, 0, err
			}
			key := strings.ToLower(name)
			if other, ok := folded[key]; ok {
				return nil, nil, 0, fmt.Errorf("zip entries %s and %s collide ignoring case", name, other)
			}
			if _, ok := entries[name]; ok {
				return nil, nil, 0, fmt.Errorf("duplicate zip entry: %s", name)
			}
			folded[key] = name
			entries[name] = f
		}
	}
	if manifestEntry == nil {
		return nil, nil, 0, fmt.Errorf("zip must contain exactly one root manifest.json")
	}

	// Cheap pre-check against declared sizes so a forged huge header fails
	// before any decompression begins.
	var declaredTotal int64
	countDeclared := func(declared uint64) error {
		declaredTotal += int64(declared)
		if declaredTotal > opts.MaxBytes {
			return fmt.Errorf("uncompressed size exceeds limit %d bytes", opts.MaxBytes)
		}
		return nil
	}
	if err := countDeclared(manifestEntry.UncompressedSize64); err != nil {
		return nil, nil, 0, err
	}
	for _, f := range entries {
		if err := countDeclared(f.UncompressedSize64); err != nil {
			return nil, nil, 0, err
		}
	}

	var total int64
	raw, err := readLimited(manifestEntry, opts.MaxBytes, &total)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("cannot read manifest.json: %w", err)
	}
	return entries, raw, total, nil
}

// readLimited streams a single entry through the SHA-256 hasher while
// enforcing the cumulative uncompressed byte budget.
func readLimited(f *zip.File, maxBytes int64, total *int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	buf := make([]byte, 0, f.UncompressedSize64)
	chunk := make([]byte, 32*1024)
	for {
		n, readErr := rc.Read(chunk)
		if n > 0 {
			*total += int64(n)
			if *total > maxBytes {
				return nil, fmt.Errorf("uncompressed size exceeds limit %d bytes", maxBytes)
			}
			buf = append(buf, chunk[:n]...)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return buf, nil
}

func matchEntries(m *Manifest, entries map[string]*zip.File) error {
	declared := make(map[string]bool, len(m.Files))
	for i := range m.Files {
		p := m.Files[i].Path
		if declared[p] {
			return fmt.Errorf("manifest lists duplicate path: %s", p)
		}
		declared[p] = true
		if _, ok := entries[p]; !ok {
			return fmt.Errorf("manifest declares %s but no matching zip entry", p)
		}
	}
	for name := range entries {
		if !declared[name] {
			return fmt.Errorf("zip contains extra entry not listed in manifest: %s", name)
		}
	}
	return nil
}

func extractAll(zr *zip.Reader, m *Manifest, opts Options, total int64) (map[string]*extractedFile, error) {
	lookup := make(map[string]*zip.File, len(zr.File))
	for i := range zr.File {
		f := zr.File[i]
		lookup[f.Name] = f
	}

	out := make(map[string]*extractedFile, len(m.Files))
	for i := range m.Files {
		decl := m.Files[i]
		f := lookup[decl.Path]

		var data bytes.Buffer
		h := sha256.New()
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("cannot read %s: %w", decl.Path, err)
		}
		pr := &countingReader{r: rc, total: &total, max: opts.MaxBytes}
		n, copyErr := io.Copy(io.MultiWriter(h, &data), pr)
		closeErr := rc.Close()
		if copyErr != nil {
			return nil, fmt.Errorf("cannot read %s: %w", decl.Path, copyErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("zip integrity check failed for %s: %w", decl.Path, closeErr)
		}

		if n != decl.Size {
			return nil, fmt.Errorf("size mismatch for %s: actual %d, manifest %d", decl.Path, n, decl.Size)
		}
		sum := h.Sum(nil)
		if !equalBytes(sum, decl.sum) {
			return nil, fmt.Errorf("sha256 mismatch for %s: actual %s, manifest %s",
				decl.Path, hex.EncodeToString(sum), decl.SHA256)
		}
		out[decl.Path] = &extractedFile{decl: decl, sum: sum, size: n, data: data.Bytes()}
	}
	return out, nil
}

type countingReader struct {
	r     io.Reader
	total *int64
	max   int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		*c.total += int64(n)
		if *c.total > c.max {
			return n, fmt.Errorf("uncompressed size exceeds limit %d bytes", c.max)
		}
	}
	return n, err
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// publishFiles writes verified data files into the staging directory and
// re-hashes them from disk before the directory is published.
func publishFiles(staging string, files map[string]*extractedFile) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		f := files[p]
		full := filepath.Join(staging, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("cannot create directory for %s: %w", p, err)
		}
		if err := writeVerified(full, f); err != nil {
			return err
		}
	}
	return nil
}

func writeVerified(full string, f *extractedFile) error {
	out, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("cannot write %s: %w", f.decl.Path, err)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), bytes.NewReader(f.data)); err != nil {
		_ = out.Close()
		return fmt.Errorf("cannot write %s: %w", f.decl.Path, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("cannot close %s: %w", f.decl.Path, err)
	}
	sum := h.Sum(nil)
	if !equalBytes(sum, f.sum) {
		return fmt.Errorf("post-write hash mismatch for %s", f.decl.Path)
	}
	return nil
}

func buildReceipt(target string, files map[string]*extractedFile) *Receipt {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	r := &Receipt{Target: target, Files: make([]FileResult, 0, len(paths))}
	for _, p := range paths {
		f := files[p]
		r.Files = append(r.Files, FileResult{
			Path:   p,
			Size:   f.size,
			SHA256: hex.EncodeToString(f.sum),
		})
		r.TotalSize += f.size
	}
	r.FileCount = len(r.Files)
	return r
}
