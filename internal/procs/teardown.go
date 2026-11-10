package procs

import (
	"syscall"
	"time"
)

// TeardownOptions bound a teardown. Zero values take the defaults.
type TeardownOptions struct {
	TermGrace time.Duration
	Bound     time.Duration
}

// TeardownResult says what a teardown did.
type TeardownResult struct {
	Killed    int
	Remaining []int
	Complete  bool
	Elapsed   time.Duration
}

// Teardown kills the child's process group. The bounded procedure that
// also hunts descendants arrives with the descendants listing.
func (p *Process) Teardown(opts TeardownOptions) TeardownResult {
	start := time.Now()
	_ = syscall.Kill(-p.pid, syscall.SIGKILL)
	<-p.done
	p.Close()
	return TeardownResult{Complete: true, Elapsed: time.Since(start)}
}
