// Command packagegate verifies a vendor-delivered ZIP package against its
// manifest and publishes the data files to a local directory only when every
// check succeeds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"example.com/vendor-package-intake/internal/gate"
)

func main() {
	zipPath := flag.String("zip", "", "path to the vendor ZIP package")
	target := flag.String("target", "", "directory to publish into (must not exist yet)")
	maxEntries := flag.Int("max-entries", 0, "maximum number of zip entries (including manifest)")
	maxBytes := flag.Int64("max-bytes", 0, "maximum total extracted bytes (including manifest)")
	flag.Parse()

	if *zipPath == "" || *target == "" || *maxEntries <= 0 || *maxBytes <= 0 {
		flag.Usage()
		failExit("all of -zip, -target, -max-entries and -max-bytes are required")
	}

	receipt, err := gate.Ingest(gate.Options{
		ZipPath:       *zipPath,
		TargetDir:     *target,
		MaxEntries:    *maxEntries,
		MaxTotalBytes: *maxBytes,
	})
	if err != nil {
		failExit("%s", err.Error())
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(receipt); err != nil {
		failExit("cannot write receipt: %v", err)
	}
}

func failExit(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "packagegate: "+format+"\n", args...)
	os.Exit(1)
}
