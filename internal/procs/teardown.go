package procs

import (
	"errors"
	"sort"
	"syscall"
	"time"
)

// TeardownOptions bound a teardown. Zero values take the defaults the spec
// names: 15 seconds from SIGTERM to SIGKILL, 60 seconds in all. The grace
// never extends past the bound.
type TeardownOptions struct {
	// TermGrace is how long the child has after SIGTERM before SIGKILL.
	TermGrace time.Duration
	// Bound caps the whole teardown, including the hunt for descendants.
	Bound time.Duration
}

const (
	defaultTermGrace = 15 * time.Second
	defaultBound     = 60 * time.Second
	// huntInterval is the pause between passes over the descendants.
	huntInterval = 50 * time.Millisecond
	// quietPasses is how many consecutive passes must find nothing alive
	// and reap nothing before the teardown is complete. One quiet pass is
	// not enough: a process that forks and exits faster than the table is
	// read shows up as nothing alive on any single pass.
	quietPasses = 3
)

func (o TeardownOptions) withDefaults() TeardownOptions {
	if o.TermGrace <= 0 {
		o.TermGrace = defaultTermGrace
	}
	if o.Bound <= 0 {
		o.Bound = defaultBound
	}
	return o
}

// TeardownResult says what a teardown did.
type TeardownResult struct {
	// Leftovers counts the processes that outlived the child, or that ran
	// outside its process group and had to be killed one by one. Above
	// zero means the child left work running, which the contract forbids.
	// A helper in the child's own group that died with the group is not a
	// leftover.
	Leftovers int
	// Remaining lists the processes still found when the bound expired.
	Remaining []int
	// Unsettled is true when processes kept appearing, or the process
	// table could not be read, until the bound expired. Something of the
	// child may still run.
	Unsettled bool
	// Complete is true when the table was quiet for quietPasses in a row.
	Complete bool
	Elapsed  time.Duration
}

// killFn is the per-process kill, replaced in tests that simulate a
// process that will not die.
var killFn = func(p *Process, pid int, sig syscall.Signal) error { return p.tr.kill(pid, sig) }

// Teardown ends the child and everything it left behind, within the bound.
// SIGTERM goes to the process group, SIGKILL after the grace, then every
// process still found is killed, pass after pass, until the table has
// been quiet for several passes or the bound expires. It is safe to call
// more than once.
func (p *Process) Teardown(opts TeardownOptions) TeardownResult {
	opts = opts.withDefaults()
	start := time.Now()
	deadline := start.Add(opts.Bound)
	leftovers := map[int]bool{}
	refused := map[int]bool{}

	// A child that was still running when teardown began ends from the
	// signals below, and a helper in its group that outlives it for a
	// moment is its own work, not a leftover.
	doneAtStart := p.childDone()
	if doneAtStart {
		// Anything alive now outlived the child.
		if alive, _, err := scanFn(p); err == nil {
			for _, lp := range alive {
				leftovers[lp.pid] = true
			}
		}
	} else {
		p.tr.killGroup(syscall.SIGTERM)
		grace := opts.TermGrace
		if left := time.Until(deadline); left < grace {
			grace = left
		}
		timer := time.NewTimer(grace)
		select {
		case <-p.done:
		case <-timer.C:
		}
		timer.Stop()
		p.tr.killGroup(syscall.SIGKILL)
	}

	quiet := 0
	unsettled := false
	var remaining []int
	for {
		remaining = remaining[:0]
		childDone := p.childDone()
		if !childDone {
			remaining = append(remaining, p.pid)
		}
		alive, reaped, err := scanFn(p)
		if err != nil {
			unsettled = true
		}
		for _, lp := range alive {
			if doneAtStart || lp.pgid != p.pid {
				leftovers[lp.pid] = true
			}
			switch kerr := killFn(p, lp.pid, syscall.SIGKILL); {
			case kerr == nil:
				remaining = append(remaining, lp.pid)
			case errors.Is(kerr, syscall.ESRCH):
				// Gone, or a different process now, between the listing
				// and the kill.
			default:
				remaining = append(remaining, lp.pid)
				refused[lp.pid] = true
			}
		}
		if err == nil && len(alive) == 0 && reaped == 0 && childDone {
			quiet++
			if quiet >= quietPasses {
				break
			}
		} else {
			quiet = 0
			if reaped > 0 {
				unsettled = true
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(huntInterval)
	}

	// A process whose kill was refused was never killed; it is remaining,
	// not a leftover that was dealt with.
	for pid := range refused {
		delete(leftovers, pid)
	}
	p.Close()
	res := TeardownResult{Leftovers: len(leftovers), Elapsed: time.Since(start)}
	if quiet >= quietPasses {
		res.Complete = true
	} else {
		res.Unsettled = unsettled || len(remaining) == 0
		res.Remaining = append([]int(nil), remaining...)
		sort.Ints(res.Remaining)
	}
	return res
}
