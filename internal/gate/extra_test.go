package gate

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// TestReceiptIndependentOfEntryOrder rebuilds the same package twice with
// different zip iteration orders and compares the resulting receipts.
func TestReceiptIndependentOfEntryOrder(t *testing.T) {
	build := func(order []string) *zipBuilder {
		contents := map[string][]byte{
			"z.txt":    []byte("zoo"),
			"a/n.txt":  []byte("nested"),
			"m.bin":    []byte("binary"),
			"manifest": nil,
		}
		entries := []manifestEntry{
			manifestEntryFor("z.txt", contents["z.txt"]),
			manifestEntryFor("a/n.txt", contents["a/n.txt"]),
			manifestEntryFor("m.bin", contents["m.bin"]),
		}
		b := newZipBuilder()
		for _, name := range order {
			if name == "manifest" {
				b.add("manifest.json", manifestJSON(t, entries))
			} else {
				b.add(name, contents[name])
			}
		}
		return b
	}

	_, r1, err := runIngest(t, build([]string{"manifest", "z.txt", "a/n.txt", "m.bin"}))
	if err != nil {
		t.Fatal(err)
	}
	_, r2, err := runIngest(t, build([]string{"m.bin", "manifest", "a/n.txt", "z.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(r1.Files) != len(r2.Files) {
		t.Fatal("receipt length differs")
	}
	for i := range r1.Files {
		if r1.Files[i] != r2.Files[i] {
			t.Fatalf("receipt differs at %d:\n%+v\n%+v", i, r1.Files[i], r2.Files[i])
		}
	}
}

// TestFailureLeavesNothingBehind verifies a rejected package neither creates
// the target nor leaves the staging directory behind in the parent.
func TestFailureLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	data := []byte("good")
	badEntry := manifestEntryFor("a.txt", []byte("evil"))
	b := newZipBuilder().
		add("manifest.json", manifestJSON(t, []manifestEntry{badEntry})).
		add("a.txt", data)
	zipPath := filepath.Join(dir, "pkg.zip")
	target := filepath.Join(dir, "published")
	b.write(t, zipPath)

	if _, err := Ingest(Options{zipPath, target, 100, 1 << 20}); err == nil {
		t.Fatal("expected rejection")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target must not exist after failure: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name := e.Name(); name != "pkg.zip" {
			t.Fatalf("unexpected leftover %q", name)
		}
	}
}

func TestIngestRejectsSymlinkEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pkg.zip")
	f, _ := os.Create(zipPath)
	defer f.Close()
	w := zip.NewWriter(f)

	mh := &zip.FileHeader{Name: "manifest.json"}
	mw, _ := w.CreateHeader(mh)
	mw.Write(manifestJSON(t, nil))

	fh := &zip.FileHeader{Name: "link"}
	fh.SetMode(os.ModeSymlink | 0o777)
	lw, _ := w.CreateHeader(fh)
	lw.Write([]byte("../target"))
	w.Close()

	target := filepath.Join(dir, "published")
	_, err := Ingest(Options{zipPath, target, 100, 1 << 20})
	if err == nil {
		t.Fatal("symlink entry must be rejected")
	}
}
