package procs

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTeardownEndsARunningChildWithTerm(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 0 || len(res.Remaining) != 0 || res.Unsettled {
		t.Fatalf("result = %+v", res)
	}
	st := waitFor(t, p, time.Second)
	if !st.Signaled || st.Signal != syscall.SIGTERM {
		t.Fatalf("status = %s, want SIGTERM", st)
	}
	if alive(p.Pid()) {
		t.Fatal("child still alive")
	}
}

func TestTeardownEscalatesToKillAfterTheGrace(t *testing.T) {
	p := startFixture(t, "ignore-term.sh", Spec{})
	// Wait until the first sleep exists, which means the trap is set.
	deadline := time.Now().Add(5 * time.Second)
	for {
		ds, _ := p.Descendants()
		if len(ds) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	start := time.Now()
	res := p.Teardown(quick)
	if !res.Complete {
		t.Fatalf("result = %+v", res)
	}
	if d := time.Since(start); d < quick.TermGrace || d > quick.Bound {
		t.Fatalf("teardown took %s; want between the grace and the bound", d)
	}
	st := waitFor(t, p, time.Second)
	if !st.Signaled || st.Signal != syscall.SIGKILL {
		t.Fatalf("status = %s, want SIGKILL", st)
	}
}

func TestTeardownCountsWorkTheChildLeftBehind(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "leave-background.sh", Spec{Stdout: &out})
	waitFor(t, p, 10*time.Second)
	left, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	descendantsSoon(t, p, left, 3*time.Second)
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 1 || len(res.Remaining) != 0 {
		t.Fatalf("result = %+v, want one leftover", res)
	}
	if !gone(left, 2*time.Second) {
		t.Fatalf("leftover %d still alive", left)
	}
}

func TestTeardownCountsALeftoverDuringThePipeCut(t *testing.T) {
	// The child has exited but Wait has not returned, because a grandchild
	// holds stdout. Teardown must still see the child as done and count
	// the holder.
	var out bytes.Buffer
	p := startFixture(t, "hold-stdout.sh", Spec{Stdout: NewCappedWriter(&out, 1<<20)})
	time.Sleep(500 * time.Millisecond)
	if p.Exited() {
		t.Skip("Wait already returned; the window closed")
	}
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 1 {
		t.Fatalf("result = %+v, want one leftover", res)
	}
}

func TestTeardownOfARunningChildDoesNotCountItsOwnHelpers(t *testing.T) {
	p := startFixture(t, "trap-helper.sh", Spec{})
	deadline := time.Now().Add(5 * time.Second)
	for {
		ds, _ := p.Descendants()
		if len(ds) >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 0 {
		t.Fatalf("result = %+v; a helper in the child's group is not a leftover", res)
	}
}

func TestTeardownOnAnExitedChildWithNothingLeftIsClean(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	res := p.Teardown(quick)
	if !res.Complete || res.Leftovers != 0 || len(res.Remaining) != 0 || res.Unsettled {
		t.Fatalf("result = %+v", res)
	}
}

func TestTeardownIsIdempotent(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	a := p.Teardown(quick)
	b := p.Teardown(quick)
	if !a.Complete || !b.Complete {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
}

func TestTeardownReportsWhatItCouldNotKill(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	oldScan, oldKill := scanFn, killFn
	t.Cleanup(func() { scanFn, killFn = oldScan, oldKill })
	scanFn = func(p *Process) ([]liveProc, int, error) { return []liveProc{{pid: 1, pgid: 1}}, 0, nil }
	killFn = func(p *Process, pid int, sig syscall.Signal) error {
		if pid == 1 {
			return syscall.EPERM
		}
		return p.tr.kill(pid, sig)
	}
	start := time.Now()
	res := p.Teardown(TeardownOptions{TermGrace: 50 * time.Millisecond, Bound: 300 * time.Millisecond})
	if res.Complete || len(res.Remaining) != 1 || res.Remaining[0] != 1 {
		t.Fatalf("result = %+v, want incomplete with pid 1 remaining", res)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Fatalf("teardown took %s, want about the bound", d)
	}
	if res.Leftovers != 0 {
		t.Fatalf("Leftovers = %d; a refused kill is not a kill", res.Leftovers)
	}
}

func TestTeardownIsIncompleteWhenTheListingFails(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	old := scanFn
	t.Cleanup(func() { scanFn = old })
	scanFn = func(p *Process) ([]liveProc, int, error) { return nil, 0, errors.New("table unreadable") }
	res := p.Teardown(TeardownOptions{TermGrace: 50 * time.Millisecond, Bound: 300 * time.Millisecond})
	if res.Complete || !res.Unsettled {
		t.Fatalf("result = %+v, want incomplete and unsettled", res)
	}
}

func TestTeardownGraceIsCappedByTheBound(t *testing.T) {
	p := startFixture(t, "ignore-term.sh", Spec{})
	start := time.Now()
	res := p.Teardown(TeardownOptions{TermGrace: 10 * time.Second, Bound: 1500 * time.Millisecond})
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("teardown took %s with a 1.5 s bound", d)
	}
	if !res.Complete {
		t.Fatalf("result = %+v; SIGKILL within the bound should end an ignoring child", res)
	}
}

func TestTeardownDefaults(t *testing.T) {
	o := TeardownOptions{}.withDefaults()
	if o.TermGrace != 15*time.Second || o.Bound != 60*time.Second {
		t.Fatalf("defaults = %+v", o)
	}
	o = TeardownOptions{TermGrace: time.Second, Bound: 2 * time.Second}.withDefaults()
	if o.TermGrace != time.Second || o.Bound != 2*time.Second {
		t.Fatalf("explicit values changed: %+v", o)
	}
}
