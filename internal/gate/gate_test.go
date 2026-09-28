package gate

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipBuilder struct {
	files    map[string][]byte
	rawDirs  []string
	rawModes map[string]os.FileMode
}

func newZipBuilder() *zipBuilder {
	return &zipBuilder{files: map[string][]byte{}, rawModes: map[string]os.FileMode{}}
}

func (b *zipBuilder) add(name string, content []byte) *zipBuilder {
	b.files[name] = content
	return b
}

func (b *zipBuilder) addDir(name string) *zipBuilder {
	b.rawDirs = append(b.rawDirs, name)
	return b
}

func (b *zipBuilder) write(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, content := range b.files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if mode, ok := b.rawModes[name]; ok {
			h.SetMode(mode)
		}
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range b.rawDirs {
		if _, err := w.Create(name + "/"); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func manifestJSON(t *testing.T, entries []manifestEntry) []byte {
	t.Helper()
	data, err := json.Marshal(manifestFile{Files: entries})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func manifestEntryFor(path string, content []byte) manifestEntry {
	sum := sha256.Sum256(content)
	return manifestEntry{Path: path, Length: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
}

func runIngest(t *testing.T, b *zipBuilder) (string, *Receipt, error) {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.zip")
	target := filepath.Join(dir, "published")
	b.write(t, zipPath)
	r, err := Ingest(Options{
		ZipPath:       zipPath,
		TargetDir:     target,
		MaxEntries:    100,
		MaxTotalBytes: 1 << 20,
	})
	return target, r, err
}

func TestIngestSuccessAndReceipt(t *testing.T) {
	dataA := []byte("alpha content")
	dataB := []byte{0, 1, 2, 3, 255}
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{
			manifestEntryFor("data/a.bin", dataA),
			manifestEntryFor("b.txt", dataB),
		})).
		add("data/a.bin", dataA).
		add("b.txt", dataB)

	target, receipt, err := runIngest(t, b)
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if receipt.FileCount != 2 {
		t.Fatalf("file count = %d, want 2", receipt.FileCount)
	}
	wantTotal := int64(len(dataA) + len(dataB))
	if receipt.TotalBytes != wantTotal {
		t.Fatalf("total bytes = %d, want %d", receipt.TotalBytes, wantTotal)
	}
	if receipt.Files[0].Path != "b.txt" || receipt.Files[1].Path != "data/a.bin" {
		t.Fatalf("receipt not path-sorted: %+v", receipt.Files)
	}
	got, err := os.ReadFile(filepath.Join(target, "data", "a.bin"))
	if err != nil || string(got) != string(dataA) {
		t.Fatalf("published content mismatch: %v", err)
	}
}

func TestIngestEmptyDataAllowed(t *testing.T) {
	b := newZipBuilder().add("manifest.json", manifestJSON(t, []manifestEntry{}))
	target, receipt, err := runIngest(t, b)
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if receipt.FileCount != 0 || receipt.TotalBytes != 0 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("target dir should be published: %v", err)
	}
}

func TestIngestSHA256Mismatch(t *testing.T) {
	data := []byte("hello")
	bad := manifestEntryFor("a.txt", []byte("other"))
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{bad})).
		add("a.txt", data)
	_, _, err := runIngest(t, b)
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha256 mismatch, got %v", err)
	}
}

func TestIngestLengthMismatch(t *testing.T) {
	data := []byte("hello")
	entry := manifestEntryFor("a.txt", data)
	entry.Length++
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{entry})).
		add("a.txt", data)
	_, _, err := runIngest(t, b)
	if err == nil || !strings.Contains(err.Error(), "length mismatch") {
		t.Fatalf("want length mismatch, got %v", err)
	}
}

func TestIngestPathTraversal(t *testing.T) {
	for _, bad := range []string{
		"../evil.txt",
		"a/../../b.txt",
		`a\b.txt`,
		"/abs.txt",
		"C:/x.txt",
		"a//b.txt",
		"a/./b.txt",
		"a/../b.txt",
		"",
		"a/b/",
	} {
		data := []byte("x")
		entry := manifestEntryFor("p", data)
		entry.Path = bad
		b := newZipBuilder().add("manifest.json", manifestJSON(t, []manifestEntry{entry}))
		if bad == "" {
			// no corresponding zip entry; empty path must be rejected from manifest alone
		} else if strings.HasSuffix(bad, "/") {
			b.addDir(strings.TrimSuffix(bad, "/"))
		} else {
			b.add(bad, data)
		}
		_, _, err := runIngest(t, b)
		if err == nil {
			t.Fatalf("path %q: want rejection, got success", bad)
		}
	}
}

func TestIngestCaseInsensitiveAndFileDirConflict(t *testing.T) {
	data := []byte("x")
	t.Run("case collision", func(t *testing.T) {
		entries := []manifestEntry{
			manifestEntryFor("A.txt", data),
			manifestEntryFor("a.TXT", data),
		}
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, entries)).
			add("A.txt", data).add("a.TXT", data)
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "case") {
			t.Fatalf("want case collision, got %v", err)
		}
	})
	t.Run("file subtree conflict", func(t *testing.T) {
		entries := []manifestEntry{
			manifestEntryFor("a", data),
			manifestEntryFor("a/b.txt", data),
		}
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, entries)).
			add("a", data).add("a/b.txt", data)
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "subdirectory") {
			t.Fatalf("want subtree conflict, got %v", err)
		}
	})
}

func TestIngestMissingAndExtraFiles(t *testing.T) {
	data := []byte("x")
	t.Run("missing declared file", func(t *testing.T) {
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, []manifestEntry{manifestEntryFor("a.txt", data)}))
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "missing") {
			t.Fatalf("want missing error, got %v", err)
		}
	})
	t.Run("extra zip entry", func(t *testing.T) {
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, []manifestEntry{manifestEntryFor("a.txt", data)})).
			add("a.txt", data).add("extra.txt", data)
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "not listed") {
			t.Fatalf("want extra-entry error, got %v", err)
		}
	})
	t.Run("duplicate manifest declaration", func(t *testing.T) {
		entries := []manifestEntry{
			manifestEntryFor("a.txt", data),
			manifestEntryFor("a.txt", data),
		}
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, entries)).
			add("a.txt", data)
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("want duplicate error, got %v", err)
		}
	})
}

func TestIngestLimits(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 512)
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{manifestEntryFor("a.bin", data)})).
		add("a.bin", data)
	zipPath := filepath.Join(dir, "pkg.zip")
	target := filepath.Join(dir, "published")
	b.write(t, zipPath)

	t.Run("entry limit", func(t *testing.T) {
		_, err := Ingest(Options{zipPath, target + "-e", 1, 1 << 20})
		if err == nil || !strings.Contains(err.Error(), "limit is") {
			t.Fatalf("want entry-limit error, got %v", err)
		}
	})
	t.Run("byte limit", func(t *testing.T) {
		_, err := Ingest(Options{zipPath, target + "-b", 100, 100})
		if err == nil || !strings.Contains(err.Error(), "byte limit") {
			t.Fatalf("want byte-limit error, got %v", err)
		}
	})
}

func TestIngestTargetProtection(t *testing.T) {
	data := []byte("ok")
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{manifestEntryFor("a.txt", data)})).
		add("a.txt", data)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.zip")
	b.write(t, zipPath)

	t.Run("missing parent", func(t *testing.T) {
		_, err := Ingest(Options{zipPath, filepath.Join(dir, "nope", "pub"), 100, 1 << 20})
		if err == nil || !strings.Contains(err.Error(), "parent") {
			t.Fatalf("want parent error, got %v", err)
		}
	})
	t.Run("existing directory", func(t *testing.T) {
		existing := filepath.Join(dir, "exists")
		if err := os.Mkdir(existing, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Ingest(Options{zipPath, existing, 100, 1 << 20})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("want already-exists error, got %v", err)
		}
	})
	t.Run("existing file", func(t *testing.T) {
		file := filepath.Join(dir, "afile")
		if err := os.WriteFile(file, []byte("z"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Ingest(Options{zipPath, file, 100, 1 << 20})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("want already-exists error, got %v", err)
		}
	})
}

func TestIngestRejectsDirectoriesAndBadManifest(t *testing.T) {
	t.Run("explicit directory entry", func(t *testing.T) {
		b := newZipBuilder().
			add("manifest.json", manifestJSON(t, nil)).
			addDir("data")
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "director") {
			t.Fatalf("want directory error, got %v", err)
		}
	})
	t.Run("missing manifest", func(t *testing.T) {
		b := newZipBuilder().add("a.txt", []byte("x"))
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "manifest") {
			t.Fatalf("want manifest error, got %v", err)
		}
	})
	t.Run("malformed manifest", func(t *testing.T) {
		b := newZipBuilder().add("manifest.json", []byte("{not json"))
		_, _, err := runIngest(t, b)
		if err == nil || !strings.Contains(err.Error(), "invalid manifest") {
			t.Fatalf("want invalid manifest, got %v", err)
		}
	})
}
