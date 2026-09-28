package packagegate

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func expectReject(t *testing.T, entries []zipEntry, opts Options, wantSubstring string) {
	t.Helper()
	parent := t.TempDir()
	zipPath := buildZIP(t, entries)
	_, target, err := runIntake(t, zipPath, parent, opts)
	if err == nil {
		t.Fatalf("expected failure containing %q, got success", wantSubstring)
	}
	if !strings.Contains(err.Error(), wantSubstring) {
		t.Fatalf("error %q does not contain %q", err.Error(), wantSubstring)
	}
	if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
		t.Fatalf("target %s must not be created on failure", target)
	}
	leftovers, _ := os.ReadDir(parent)
	for _, info := range leftovers {
		if strings.HasPrefix(info.Name(), ".packagegate-stage-") {
			t.Fatalf("staging directory left behind: %s", info.Name())
		}
	}
}

func filesOne() []testFile {
	return []testFile{{path: "data/a.txt", data: []byte("payload")}}
}

func TestRejectDigestMismatch(t *testing.T) {
	files := filesOne()
	raw := manifestJSON(t, files)
	entries := withManifest(files, raw)
	entries[1].data = []byte("payloxd")
	expectReject(t, entries, defaultOpts(files), "sha256 mismatch")
}

func TestRejectSizeMismatch(t *testing.T) {
	files := filesOne()
	raw := manifestJSON(t, files)
	entries := withManifest(files, raw)
	entries[1].data = bytes.Repeat([]byte("x"), len("payload"))
	expectReject(t, entries, defaultOpts(files), "sha256 mismatch")

	m := Manifest{Files: []FileDecl{{
		Path:   "data/a.txt",
		Size:   999,
		SHA256: strings.Repeat("0", 64),
	}}}
	bad, _ := json.Marshal(m)
	entries = withManifest(files, bad)
	expectReject(t, entries, Options{MaxEntries: 2, MaxBytes: 4096}, "size mismatch")
}

func TestRejectUnsafePaths(t *testing.T) {
	badPaths := []string{
		"../evil.txt",
		"a/../../b.txt",
		"a\\b.txt",
		"C:/win/x.txt",
		"/abs/x.txt",
		"a//b.txt",
		"a/./b.txt",
		"a/../b.txt",
		"CON",
		"dir/LPT1.txt",
		"trailing.",
		"sp ace.txt",
	}
	for _, p := range badPaths {
		files := []testFile{{path: p, data: []byte("x")}}
		entries := []zipEntry{
			{name: "manifest.json", data: manifestJSON(t, files)},
			{name: p, data: []byte("x")},
		}
		expectReject(t, entries, Options{MaxEntries: 2, MaxBytes: 4096}, "")
	}
}

func TestRejectCaseCollisionInManifest(t *testing.T) {
	files := []testFile{
		{path: "Data/a.txt", data: []byte("1")},
		{path: "data/A.txt", data: []byte("2")},
	}
	expectReject(t, goodEntries(t, files), defaultOpts(files), "collide")
}

func TestRejectFileTreeConflict(t *testing.T) {
	files := []testFile{
		{path: "a", data: []byte("1")},
		{path: "a/b.txt", data: []byte("2")},
	}
	expectReject(t, goodEntries(t, files), defaultOpts(files), "conflict")
}

func TestRejectExtraZipEntry(t *testing.T) {
	files := filesOne()
	entries := goodEntries(t, files)
	entries = append(entries, zipEntry{name: "extra.txt", data: []byte("x")})
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "extra entry")
}

func TestRejectMissingZipEntry(t *testing.T) {
	files := filesOne()
	entries := []zipEntry{
		{name: "manifest.json", data: manifestJSON(t, files)},
	}
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "no matching zip entry")
}

func TestRejectDuplicateDeclaration(t *testing.T) {
	sum64 := strings.Repeat("a", 64)
	m := Manifest{Files: []FileDecl{
		{Path: "a.txt", Size: 1, SHA256: sum64},
		{Path: "a.txt", Size: 1, SHA256: sum64},
	}}
	raw, _ := json.Marshal(m)
	entries := []zipEntry{
		{name: "manifest.json", data: raw},
		{name: "a.txt", data: []byte("x")},
	}
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "duplicates")
}

func TestRejectNoManifest(t *testing.T) {
	entries := []zipEntry{{name: "a.txt", data: []byte("x")}}
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "manifest.json")
}

func TestRejectDirectoryAndSpecialEntries(t *testing.T) {
	files := filesOne()
	entries := goodEntries(t, files)
	entries = append(entries, zipEntry{name: "dir", isDir: true})
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "directory")

	entries = goodEntries(t, files)
	entries = append(entries, zipEntry{name: "link", mode: os.ModeSymlink, data: []byte("x")})
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 4096}, "symbolic link")
}

func TestRejectEntryAndByteLimits(t *testing.T) {
	files := filesOne()

	entries := goodEntries(t, files)
	expectReject(t, entries, Options{MaxEntries: 1, MaxBytes: 4096}, "entry count")

	big := []testFile{{path: "big.bin", data: make([]byte, 100)}}
	entries = goodEntries(t, big)
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 10}, "exceeds limit")

	entries = goodEntries(t, nil)
	expectReject(t, entries, Options{MaxEntries: 5, MaxBytes: 1}, "exceeds limit")
}

func TestRejectExistingTarget(t *testing.T) {
	files := filesOne()

	parent := t.TempDir()
	targetDir := filepath.Join(parent, "delivered")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zipPath := buildZIP(t, goodEntries(t, files))
	if _, err := Intake(zipPath, targetDir, defaultOpts(files)); err == nil {
		t.Fatal("expected failure for existing directory")
	}

	parent2 := t.TempDir()
	targetFile := filepath.Join(parent2, "delivered")
	if err := os.WriteFile(targetFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	zipPath2 := buildZIP(t, goodEntries(t, files))
	if _, err := Intake(zipPath2, targetFile, defaultOpts(files)); err == nil {
		t.Fatal("expected failure for existing file")
	}

	parent3 := t.TempDir()
	missingParent := filepath.Join(parent3, "no-such-parent", "delivered")
	zipPath3 := buildZIP(t, goodEntries(t, files))
	if _, err := Intake(zipPath3, missingParent, defaultOpts(files)); err == nil {
		t.Fatal("expected failure for missing parent directory")
	}
}

func TestReceiptOrderIndependent(t *testing.T) {
	files := []testFile{
		{path: "z.txt", data: []byte("zebra")},
		{path: "a.txt", data: []byte("apple")},
		{path: "m/n.txt", data: []byte("nested")},
	}
	entries := goodEntries(t, files)

	parent1 := t.TempDir()
	r1, _, err := runIntake(t, buildZIP(t, entries), parent1, defaultOpts(files))
	if err != nil {
		t.Fatal(err)
	}

	reversed := make([]zipEntry, len(entries))
	for i, e := range entries {
		reversed[len(entries)-1-i] = e
	}
	parent2 := t.TempDir()
	r2, _, err := runIntake(t, buildZIP(t, reversed), parent2, defaultOpts(files))
	if err != nil {
		t.Fatal(err)
	}

	b1, _ := json.Marshal(r1.Files)
	b2, _ := json.Marshal(r2.Files)
	if !bytes.Equal(b1, b2) {
		t.Fatalf("receipts differ:\n%s\n%s", b1, b2)
	}
	if r1.TotalSize != r2.TotalSize || r1.FileCount != 3 {
		t.Fatalf("summary mismatch: %+v %+v", r1, r2)
	}
}

func TestInvalidOptions(t *testing.T) {
	zipPath := buildZIP(t, goodEntries(t, nil))
	parent := t.TempDir()
	target := filepath.Join(parent, "out")
	if _, err := Intake(zipPath, target, Options{MaxEntries: 0, MaxBytes: 10}); err == nil {
		t.Fatal("expected error for non-positive max entries")
	}
	if _, err := Intake(zipPath, target, Options{MaxEntries: 10, MaxBytes: 0}); err == nil {
		t.Fatal("expected error for non-positive max bytes")
	}
}

func TestRejectCorruptZip(t *testing.T) {
	parent := t.TempDir()
	zipPath := filepath.Join(parent, "broken.zip")
	if err := os.WriteFile(zipPath, []byte("not a zip at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Intake(zipPath, filepath.Join(parent, "out"), Options{MaxEntries: 10, MaxBytes: 4096}); err == nil {
		t.Fatal("expected corrupt zip rejection")
	}
}

func TestRejectCorruptCompressedData(t *testing.T) {
	files := filesOne()
	parent := t.TempDir()
	zipPath := buildZIP(t, goodEntries(t, files))

	raw, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	offset, err := zr.File[1].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	flip := make([]byte, 4)
	if _, err := rand.Read(flip); err != nil {
		t.Fatal(err)
	}
	for i := range flip {
		raw[int(offset)+i] ^= 0xFF
	}
	corrupt := filepath.Join(parent, "corrupt.zip")
	if err := os.WriteFile(corrupt, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(parent, "out")
	if _, err := Intake(corrupt, target, defaultOpts(files)); err == nil {
		t.Fatal("expected CRC/decompression failure")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatal("target must not exist after failure")
	}
}
