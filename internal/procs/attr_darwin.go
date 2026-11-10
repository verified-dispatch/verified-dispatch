//go:build darwin

package procs

import "syscall"

// sysProcAttr puts the child in a new session and process group. macOS has
// no parent-death signal and no subreaper; descendants are polled instead.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

type platformState struct{}

func startPlatform(*Process) *platformState { return nil }

// Close releases platform resources. It is safe to call more than once.
func (p *Process) Close() {}
