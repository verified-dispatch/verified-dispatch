package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrTruncated is returned when NUL-separated output does not end in NUL,
// which means the producer was cut off.
var ErrTruncated = errors.New("gitx: NUL-separated output is truncated")

// ParseZ splits NUL-terminated git output into entries. Empty output gives
// no entries. Output that does not end in NUL is truncated and is an
// error. Entries are returned in order; callers that read paired records
// (such as a rename's two paths from status --porcelain=v2 -z) consume
// consecutive entries themselves.
func ParseZ(out []byte) ([]string, error) {
	if len(out) == 0 {
		return nil, nil
	}
	if out[len(out)-1] != 0 {
		return nil, ErrTruncated
	}
	parts := bytes.Split(out[:len(out)-1], []byte{0})
	entries := make([]string, len(parts))
	for i, p := range parts {
		entries[i] = string(p)
	}
	return entries, nil
}

// The oldest git the tool accepts. GIT_NO_LAZY_FETCH arrived in 2.44; older
// git ignores the variable silently, so a partial clone would fetch.
const (
	FloorMajor = 2
	FloorMinor = 44
)

// ErrVersionTooOld is returned when the installed git is below the floor.
var ErrVersionTooOld = errors.New("gitx: git is older than the supported floor")

// ParseVersion reads the major and minor numbers from `git version` output.
func ParseVersion(out string) (major, minor int, err error) {
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[0] != "git" || fields[1] != "version" {
		return 0, 0, fmt.Errorf("gitx: unexpected version output %q", out)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("gitx: unexpected version number %q", fields[2])
	}
	if major, err = strconv.Atoi(parts[0]); err != nil {
		return 0, 0, fmt.Errorf("gitx: unexpected major version %q", parts[0])
	}
	if minor, err = strconv.Atoi(parts[1]); err != nil {
		return 0, 0, fmt.Errorf("gitx: unexpected minor version %q", parts[1])
	}
	return major, minor, nil
}

func checkFloor(major, minor int) error {
	if major > FloorMajor || (major == FloorMajor && minor >= FloorMinor) {
		return nil
	}
	return fmt.Errorf("%w: have %d.%d, need %d.%d", ErrVersionTooOld, major, minor, FloorMajor, FloorMinor)
}

// CheckVersion runs `git version` and compares it with the floor.
func (r Runner) CheckVersion(ctx context.Context) error {
	out, err := r.Run(ctx, "version")
	if err != nil {
		return err
	}
	major, minor, err := ParseVersion(string(out))
	if err != nil {
		return err
	}
	return checkFloor(major, minor)
}
