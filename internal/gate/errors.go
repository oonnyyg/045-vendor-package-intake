package gate

import "fmt"

// GateError describes a package rejection reason. All failures surfaced to
// the CLI are GateError values so messages stay single-line and stable.
type GateError struct {
	msg string
}

func (e *GateError) Error() string { return e.msg }

func fail(format string, args ...any) error {
	return &GateError{msg: fmt.Sprintf(format, args...)}
}

func errBadPath(reason string) error {
	return &GateError{msg: "invalid path: " + reason}
}
