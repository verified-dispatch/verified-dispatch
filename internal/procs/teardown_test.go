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

var quick = TeardownOptions{TermGrace: 300 * time.Millisecond, Bound: 5 * time.Second}

func TestTeardownEndsARunningChildWithTerm(t *testing.T) {
	p := startFixture(t, "sleep-long.sh", Spec{})
	res := p.Teardown(quick)
	if !res.Complete || res.Killed != 0 || len(res.Remaining) != 0 {
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
	// A SIGTERM that lands before the script has set its trap ends it at
	// once. The first sleep starts after the trap, so wait for it.
	ready := time.Now().Add(5 * time.Second)
	for {
		ds, err := p.Descendants()
		if err != nil {
			t.Fatal(err)
		}
		if len(ds) > 0 {
			break
		}
		if time.Now().After(ready) {
			t.Fatal("ignore-term.sh never started its loop")
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
	if !res.Complete || res.Killed != 1 || len(res.Remaining) != 0 {
		t.Fatalf("result = %+v, want one leftover killed", res)
	}
	if !gone(left, 2*time.Second) {
		t.Fatalf("leftover %d still alive", left)
	}
}

func TestTeardownKillsAGrandchildHoldingThePipe(t *testing.T) {
	var out bytes.Buffer
	p := startFixture(t, "hold-stdout.sh", Spec{Stdout: &out})
	waitFor(t, p, 20*time.Second)
	holder, _ := strconv.Atoi(strings.TrimSpace(out.String()))
	descendantsSoon(t, p, holder, 3*time.Second)
	res := p.Teardown(quick)
	if !res.Complete || res.Killed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if !gone(holder, 2*time.Second) {
		t.Fatalf("holder %d still alive", holder)
	}
}

func TestTeardownOnAnExitedChildWithNothingLeftIsClean(t *testing.T) {
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	res := p.Teardown(quick)
	if !res.Complete || res.Killed != 0 || len(res.Remaining) != 0 {
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
	// Simulate a process that will not die: the listing keeps returning
	// pid 1 and the kill is refused, as it would be for init. The child is
	// started first so its cleanup teardown runs after the real listing
	// and kill are restored.
	p := startFixture(t, "exit-code.sh", Spec{})
	waitFor(t, p, 10*time.Second)
	oldList, oldKill := listFn, killFn
	t.Cleanup(func() { listFn, killFn = oldList, oldKill })
	listFn = func(p *Process) ([]int, error) { return []int{1}, nil }
	killFn = func(pid int, sig syscall.Signal) error {
		if pid == 1 {
			return syscall.EPERM
		}
		return syscall.Kill(pid, sig)
	}
	start := time.Now()
	res := p.Teardown(TeardownOptions{TermGrace: 50 * time.Millisecond, Bound: 300 * time.Millisecond})
	if res.Complete || len(res.Remaining) != 1 || res.Remaining[0] != 1 {
		t.Fatalf("result = %+v, want incomplete with pid 1 remaining", res)
	}
	if d := time.Since(start); d < 300*time.Millisecond || d > 3*time.Second {
		t.Fatalf("teardown took %s, want about the bound", d)
	}
	if res.Killed != 0 {
		t.Fatalf("Killed = %d; a refused kill is not a kill", res.Killed)
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
	if !errors.Is(syscall.ESRCH, syscall.ESRCH) {
		t.Fatal("sanity")
	}
}
