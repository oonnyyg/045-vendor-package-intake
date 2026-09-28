// Command packagegate validates a vendor delivery ZIP and publishes it to a
// local directory only when every check succeeds.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"example.com/vendor-package-intake/internal/packagegate"
)

func main() {
	zipPath := flag.String("zip", "", "path to the vendor delivery ZIP file")
	target := flag.String("target", "", "directory to create and publish into (must not exist yet)")
	maxEntries := flag.Int("max-entries", 0, "maximum number of zip entries, including manifest.json (positive integer)")
	maxBytes := flag.Int64("max-bytes", 0, "maximum total uncompressed bytes, including manifest.json (positive integer)")
	flag.Parse()

	if *zipPath == "" || *target == "" || *maxEntries <= 0 || *maxBytes <= 0 {
		fmt.Fprintln(os.Stderr, "usage: packagegate -zip package.zip -target ./delivered -max-entries N -max-bytes N")
		flag.PrintDefaults()
		os.Exit(2)
	}

	receipt, err := packagegate.Intake(*zipPath, *target, packagegate.Options{
		MaxEntries: *maxEntries,
		MaxBytes:   *maxBytes,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "package rejected:", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(receipt); err != nil {
		fmt.Fprintln(os.Stderr, "cannot write receipt:", err)
		os.Exit(1)
	}
}
