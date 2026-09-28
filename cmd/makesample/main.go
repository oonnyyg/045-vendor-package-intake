// Command makesample builds a demo vendor package suitable for packagegate.
// It produces sample.zip containing manifest.json plus a couple of data files,
// and prints the manifest it embedded.
package main

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
)

type sampleEntry struct {
	Path   string `json:"path"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

type sampleManifest struct {
	Files []sampleEntry `json:"files"`
}

func main() {
	out := flag.String("out", "sample.zip", "output zip path")
	flag.Parse()

	files := map[string]string{
		"docs/readme.txt":  "Vendor delivery sample.\n",
		"data/numbers.csv": "1,2,3\n4,5,6\n",
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	manifest := sampleManifest{Files: []sampleEntry{}}
	for _, name := range names {
		content := []byte(files[name])
		sum := sha256.Sum256(content)
		manifest.Files = append(manifest.Files, sampleEntry{
			Path:   name,
			Length: int64(len(content)),
			SHA256: hex.EncodeToString(sum[:]),
		})
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail("encode manifest: %v", err)
	}

	f, err := os.Create(*out)
	if err != nil {
		fail("create output: %v", err)
	}
	defer f.Close()

	w := zip.NewWriter(f)
	writeEntry := func(name string, content []byte) {
		fw, err := w.Create(name)
		if err != nil {
			fail("zip create %s: %v", name, err)
		}
		if _, err := fw.Write(content); err != nil {
			fail("zip write %s: %v", name, err)
		}
	}
	writeEntry("manifest.json", append(manifestBytes, '\n'))
	for _, name := range names {
		writeEntry(name, []byte(files[name]))
	}
	if err := w.Close(); err != nil {
		fail("close zip: %v", err)
	}

	fmt.Printf("wrote %s with manifest:\n%s\n", *out, manifestBytes)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "makesample: "+format+"\n", args...)
	os.Exit(1)
}
