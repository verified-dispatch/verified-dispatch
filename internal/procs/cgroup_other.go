//go:build !linux

package procs

import (
	"errors"
	"syscall"
	"time"
)

// cgroup has no meaning outside Linux.
type cgroup struct{ dir string }

func newCgroup() *cgroup { return nil }

// The methods below exist so the shared code compiles. newCgroup never
// returns a cgroup here, so they never run.

func (c *cgroup) openFD() (int, error) { return -1, errors.ErrUnsupported }

func (c *cgroup) remove(time.Duration) error { return nil }

func cloneIntoCgroup(*syscall.SysProcAttr, int) {}
