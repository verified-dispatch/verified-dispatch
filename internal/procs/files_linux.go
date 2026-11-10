//go:build linux

package procs

import (
	"errors"
	"syscall"
)

// close_range(2) and its flag, from <linux/close_range.h>. The system call
// number is 436 on every 64-bit Linux architecture Go supports.
const (
	sysCloseRange     = 436
	closeRangeCloexec = 1 << 2
	highestDescriptor = ^uintptr(0)
)

// closeOnExecAll uses close_range when the kernel has it (5.11 and later)
// and falls back to marking each descriptor listed in /proc/self/fd.
func closeOnExecAll() error {
	_, _, e := syscall.Syscall(sysCloseRange, 3, highestDescriptor, closeRangeCloexec)
	if e == 0 {
		return nil
	}
	if !errors.Is(e, syscall.ENOSYS) && !errors.Is(e, syscall.EINVAL) {
		return e
	}
	return markFrom("/proc/self/fd")
}
