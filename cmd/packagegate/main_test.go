package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func buildCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "packagegate.exe")
	if runtime.GOOS != "windows" {
		binary = filepath.Join(t.TempDir(), "packagegate")
	}
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestCLIEndToEnd(t *testing.T) {
	binary := buildCLI(t)

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	sample := buildSample(t)

	parent := t.TempDir()
	target := filepath.Join(parent, "delivered")

	ok := exec.Command(binary,
		"-zip", sample,
		"-target", target,
		"-max-entries", "10",
		"-max-bytes", "100000",
	)
	ok.Dir = root
	stdout, err := ok.Output()
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	var receipt map[string]any
	if err := json.Unmarshal(stdout, &receipt); err != nil {
		t.Fatalf("stdout is not JSON: %v: %s", err, stdout)
	}

	bad := exec.Command(binary,
		"-zip", sample,
		"-target", target,
		"-max-entries", "10",
		"-max-bytes", "100000",
	)
	bad.Dir = root
	if err := bad.Run(); err == nil {
		t.Fatal("existing target must fail")
	}

	missingArgs := exec.Command(binary, "-zip", sample)
	if err := missingArgs.Run(); err == nil {
		t.Fatal("missing flags must fail with non-zero exit")
	}
}
