package packagegate

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type zipEntry struct {
	name  string
	data  []byte
	isDir bool
	mode  os.FileMode
}

func buildZIP(t *testing.T, entries []zipEntry) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "package.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.isDir {
			if !strings.HasSuffix(hdr.Name, "/") {
				hdr.Name += "/"
			}
			hdr.SetMode(os.ModeDir | 0o755)
		} else {
			hdr.SetMode(0o644 | e.mode)
		}
		w2, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if !e.isDir {
			if _, err := w2.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

type testFile struct {
	path string
	data []byte
}

func manifestJSON(t *testing.T, files []testFile) []byte {
	t.Helper()
	m := Manifest{Files: []FileDecl{}}
	for _, f := range files {
		sum := sha256.Sum256(f.data)
		m.Files = append(m.Files, FileDecl{
			Path:   f.path,
			Size:   int64(len(f.data)),
			SHA256: hex.EncodeToString(sum[:]),
		})
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func goodEntries(t *testing.T, files []testFile) []zipEntry {
	return withManifest(files, manifestJSON(t, files))
}

func withManifest(files []testFile, rawManifest []byte) []zipEntry {
	entries := make([]zipEntry, 0, len(files)+1)
	entries = append(entries, zipEntry{name: "manifest.json", data: rawManifest})
	for _, f := range files {
		entries = append(entries, zipEntry{name: f.path, data: f.data})
	}
	return entries
}

func runIntake(t *testing.T, zipPath string, parent string, opts Options) (*Receipt, string, error) {
	t.Helper()
	target := filepath.Join(parent, "delivered")
	r, err := Intake(zipPath, target, opts)
	return r, target, err
}

func defaultOpts(files []testFile) Options {
	var total int64
	for _, f := range files {
		total += int64(len(f.data))
	}
	return Options{MaxEntries: len(files) + 1, MaxBytes: total + 4096}
}

func TestIntakeSuccess(t *testing.T) {
	files := []testFile{
		{path: "data/hello.txt", data: []byte("hello world")},
		{path: "empty.bin", data: []byte{}},
		{path: "nested/deep/payload.bin", data: []byte{1, 2, 3, 4}},
	}
	parent := t.TempDir()
	zipPath := buildZIP(t, goodEntries(t, files))

	r, target, err := runIntake(t, zipPath, parent, defaultOpts(files))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FileCount != 3 {
		t.Fatalf("file count = %d, want 3", r.FileCount)
	}
	var wantTotal int64 = 11 + 0 + 4
	if r.TotalSize != wantTotal {
		t.Fatalf("total size = %d, want %d", r.TotalSize, wantTotal)
	}
	wantPaths := []string{"data/hello.txt", "empty.bin", "nested/deep/payload.bin"}
	sort.Strings(wantPaths)
	for i, p := range wantPaths {
		if r.Files[i].Path != p {
			t.Fatalf("files not sorted: %v", r.Files)
		}
	}
	for _, f := range files {
		got, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(f.path)))
		if err != nil {
			t.Fatalf("published file missing %s: %v", f.path, err)
		}
		if !bytes.Equal(got, f.data) {
			t.Fatalf("content mismatch for %s", f.path)
		}
	}
}

func TestIntakeEmptyManifest(t *testing.T) {
	parent := t.TempDir()
	raw := manifestJSON(t, nil)
	zipPath := buildZIP(t, withManifest(nil, raw))

	r, target, err := runIntake(t, zipPath, parent, Options{MaxEntries: 1, MaxBytes: int64(len(raw))})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.FileCount != 0 || r.TotalSize != 0 {
		t.Fatalf("unexpected receipt: %+v", r)
	}
	infos, err := os.ReadDir(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("target should contain no files, got %v", infos)
	}
}
