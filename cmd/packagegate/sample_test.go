package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type sampleFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type sampleManifest struct {
	Files []sampleFile `json:"files"`
}

// buildSample creates a valid delivery zip in a temp dir and returns its path.
func buildSample(t *testing.T) string {
	t.Helper()
	files := map[string][]byte{
		"a.txt":         []byte("alpha"),
		"dir/b.bin":     {1, 2, 3, 4, 5},
		"dir/sub/c.txt": []byte("nested"),
	}

	zipPath := filepath.Join(t.TempDir(), "sample.zip")
	out, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()

	w := zip.NewWriter(out)
	paths := make([]string, 0, len(files))
	mf := sampleManifest{Files: []sampleFile{}}
	for p, data := range files {
		paths = append(paths, p)
		sum := sha256.Sum256(data)
		mf.Files = append(mf.Files, sampleFile{
			Path:   p,
			Size:   int64(len(data)),
			SHA256: hex.EncodeToString(sum[:]),
		})
	}

	mw, err := w.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(mw).Encode(mf); err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		fw, err := w.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(files[p]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return zipPath
}
