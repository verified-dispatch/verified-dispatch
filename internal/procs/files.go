package procs

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// CloseOnExecAll marks every descriptor from 3 upward close-on-exec, so a
// child receives only the standard streams this package gives it. The tool
// can inherit descriptors from whatever started it (a make jobserver, a
// wrapper, a shell substitution), and without this they would reach the
// builder. Descriptors the Go runtime opens are already close-on-exec;
// marking them again is harmless.
func CloseOnExecAll() error { return closeOnExecAll() }

func fcntlGetfd(fd int) (int, error) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if e != 0 {
		return 0, e
	}
	return int(r), nil
}

func setCloexec(fd int) error {
	flags, err := fcntlGetfd(fd)
	if err != nil {
		return err
	}
	_, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFD, uintptr(flags|syscall.FD_CLOEXEC))
	if e != 0 {
		return e
	}
	return nil
}

// markFrom marks every descriptor listed in dir (one entry per open
// descriptor) from 3 upward. Entries that are not numbers, or that vanish
// between listing and marking, are skipped.
func markFrom(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("procs: list descriptors: %w", err)
	}
	for _, e := range entries {
		fd, err := strconv.Atoi(e.Name())
		if err != nil || fd < 3 {
			continue
		}
		if err := setCloexec(fd); err != nil && !errors.Is(err, syscall.EBADF) {
			return fmt.Errorf("procs: mark descriptor %d: %w", fd, err)
		}
	}
	return nil
}
