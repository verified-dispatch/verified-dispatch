package procs

import (
	"errors"
	"sort"
	"syscall"
	"time"
)

// TeardownOptions bound a teardown. Zero values take the defaults the spec
// names: 15 seconds from SIGTERM to SIGKILL, 60 seconds in all.
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
	// Killed counts the processes other than the child itself that were
	// alive when teardown started after the child had exited, or that had
	// to be killed one by one. Above zero means the child left work
	// running, which the contract forbids.
	Killed int
	// Remaining lists the processes still alive when the bound expired.
	Remaining []int
	// Complete is true when nothing remains.
	Complete bool
	Elapsed  time.Duration
}

// listFn and killFn are the real listing and kill, replaced in tests that
// simulate a process that will not die.
var (
	listFn = func(p *Process) ([]int, error) { return p.Descendants() }
	killFn = syscall.Kill
)

// Teardown ends the child and everything it left behind, within the bound:
// SIGTERM to the process group, SIGKILL after the grace, then every
// descendant still found is killed, pass after pass, until none is found
// or the bound expires. It is safe to call more than once.
func (p *Process) Teardown(opts TeardownOptions) TeardownResult {
	opts = opts.withDefaults()
	start := time.Now()
	deadline := start.Add(opts.Bound)
	killed := map[int]bool{}

	// Work the child left behind is counted before any signal, while the
	// group kill has not yet taken it.
	if p.Exited() {
		if ds, err := listFn(p); err == nil {
			for _, pid := range ds {
				killed[pid] = true
			}
		}
	} else {
		_ = killFn(-p.pid, syscall.SIGTERM)
		grace := time.NewTimer(opts.TermGrace)
		select {
		case <-p.done:
		case <-grace.C:
		}
		grace.Stop()
	}
	_ = killFn(-p.pid, syscall.SIGKILL)

	var remaining, refused []int
	for {
		remaining = remaining[:0]
		refused = refused[:0]
		if !p.Exited() {
			remaining = append(remaining, p.pid)
		}
		if ds, err := listFn(p); err == nil {
			for _, pid := range ds {
				err := killFn(pid, syscall.SIGKILL)
				switch {
				case err == nil:
					killed[pid] = true
					remaining = append(remaining, pid)
				case errors.Is(err, syscall.ESRCH):
					// Gone between the listing and the kill.
				default:
					remaining = append(remaining, pid)
					refused = append(refused, pid)
				}
			}
		}
		if len(remaining) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(huntInterval)
	}

	// A leftover counted up front whose kill is still refused at the end
	// was never killed, so it is reported as remaining, not as killed.
	for _, pid := range refused {
		delete(killed, pid)
	}
	p.Close()
	res := TeardownResult{Killed: len(killed), Elapsed: time.Since(start)}
	if len(remaining) == 0 {
		res.Complete = true
	} else {
		res.Remaining = append([]int(nil), remaining...)
		sort.Ints(res.Remaining)
	}
	return res
}
