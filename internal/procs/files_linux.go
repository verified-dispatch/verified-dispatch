//go:build linux

package procs

import (
	"errors"

	"golang.org/x/sys/unix"
)

// closeOnExecAll uses close_range when the kernel has it (5.11 and later)
// and falls back to marking each descriptor listed in /proc/self/fd.
func closeOnExecAll() error {
	err := unix.CloseRange(3, ^uint(0), unix.CLOSE_RANGE_CLOEXEC)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.EINVAL) {
		return err
	}
	return markFrom("/proc/self/fd")
}
