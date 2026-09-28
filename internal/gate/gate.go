package gate

import (
	"io"
)

const manifestName = "manifest.json"

// Options holds the intake limits and locations.
type Options struct {
	ZipPath       string
	TargetDir     string
	MaxEntries    int
	MaxTotalBytes int64
}

// FileResult is one verified data file in the receipt.
type FileResult struct {
	Path   string `json:"path"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

// Receipt is the JSON document printed on success.
type Receipt struct {
	Target     string       `json:"target"`
	FileCount  int          `json:"file_count"`
	TotalBytes int64        `json:"total_bytes"`
	Files      []FileResult `json:"files"`
}

type manifestEntry struct {
	Path   string `json:"path"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

type manifestFile struct {
	Files []manifestEntry `json:"files"`
}

// limitedReader aborts as soon as the cumulative extracted byte count would
// exceed the configured ceiling. Counting happens while bytes are read, so an
// oversized entry can never be fully expanded before being rejected.
type limitedReader struct {
	r         io.Reader
	total     *int64
	remaining int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remaining <= 0 {
		return 0, fail("extracted byte limit exceeded")
	}
	if int64(len(p)) > l.remaining {
		p = p[:l.remaining]
	}
	n, err := l.r.Read(p)
	*l.total += int64(n)
	l.remaining -= int64(n)
	return n, err
}
