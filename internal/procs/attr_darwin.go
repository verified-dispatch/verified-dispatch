//go:build darwin

package procs

import (
	"fmt"
	"syscall"
)

// sysProcAttr puts the child in a new session and process group. macOS has
// no parent-death signal and no subreaper; descendants are polled instead.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

type platformState struct{}

func startPlatform(*Process) *platformState { return nil }

// Close releases platform resources. It is safe to call more than once.
func (p *Process) Close() {}

// ptDenyAttach is the ptrace request that refuses later attachment, from
// <sys/ptrace.h>.
const ptDenyAttach = 31

// EnableSubreaper has no equivalent on macOS. Descendants are observed by
// polling instead; see Containment.
func EnableSubreaper() error { return nil }

// DisableAttach stops a debugger from attaching to this process. If one is
// attached already the kernel ends the process, which is the right outcome
// for a tool that holds a key in memory.
func DisableAttach() error {
	_, _, e := syscall.Syscall6(syscall.SYS_PTRACE, ptDenyAttach, 0, 0, 0, 0, 0)
	if e != 0 {
		return fmt.Errorf("procs: ptrace(PT_DENY_ATTACH): %w", e)
	}
	return nil
}

// Containment is the receipt line that says how children are contained on
// this platform.
func Containment() string {
	return "containment: process group plus observed descendants (1 s poll)"
}
