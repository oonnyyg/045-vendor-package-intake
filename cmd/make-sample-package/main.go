// Command make-sample-package builds a valid vendor delivery ZIP from a
// source directory. It walks src, adds every regular file as a zip entry
// using forward-slash relative paths, and writes a root manifest.json that
// lists each file's byte length and SHA-256 digest.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type fileDecl struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	Files []fileDecl `json:"files"`
}

func main() {
	src := flag.String("src", "", "source directory containing data files")
	out := flag.String("out", "", "output zip path")
	flag.Parse()
	if *src == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: make-sample-package -src ./delivery-src -out ./delivery.zip")
		os.Exit(2)
	}
	if err := build(*src, *out); err != nil {
		fmt.Fprintln(os.Stderr, "cannot build sample package:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", *out)
}

func build(src, out string) error {
	var rels []string
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("refusing to package non-regular file: %s", path)
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "manifest.json" || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("reserved or escaping path: %s", rel)
		}
		rels = append(rels, rel)
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(rels)

	mf := manifest{Files: make([]fileDecl, 0, len(rels))}
	data := make(map[string][]byte, len(rels))
	for _, rel := range rels {
		raw, err := os.ReadFile(filepath.Join(src, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		mf.Files = append(mf.Files, fileDecl{
			Path:   rel,
			Size:   int64(len(raw)),
			SHA256: hex.EncodeToString(sum[:]),
		})
		data[rel] = raw
	}

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	manifestWriter, err := w.CreateHeader(&zip.FileHeader{Name: "manifest.json", Method: zip.Deflate})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(manifestWriter)
	enc.SetIndent("", "  ")
	if err := enc.Encode(mf); err != nil {
		return err
	}
	for _, rel := range rels {
		entryWriter, err := w.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Deflate})
		if err != nil {
			return err
		}
		if _, err := io.Copy(entryWriter, bytes.NewReader(data[rel])); err != nil {
			return err
		}
	}
	return w.Close()
}
