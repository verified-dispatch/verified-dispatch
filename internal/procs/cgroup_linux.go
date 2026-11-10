//go:build linux

package procs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// cgroupMount is where the unified hierarchy is mounted.
const cgroupMount = "/sys/fs/cgroup"

// freezeWait bounds how long the per-member kill waits for the freeze to
// take hold, on a kernel without cgroup.kill.
const freezeWait = time.Second

// parseCgroupFile returns the directory of the unified (v2) cgroup named
// in a /proc/<pid>/cgroup file. A file with only v1 lines has no "0::"
// entry and is an error.
func parseCgroupFile(b []byte) (string, error) {
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join(cgroupMount, rel), nil
		}
	}
	return "", errors.New("procs: no cgroup v2 entry in /proc/self/cgroup")
}

// ownCgroupDir is the tool's own cgroup directory, which must be a cgroup
// v2 directory.
func ownCgroupDir() (string, error) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", fmt.Errorf("procs: read own cgroup: %w", err)
	}
	dir, err := parseCgroupFile(b)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(dir, "cgroup.procs")); err != nil {
		return "", fmt.Errorf("procs: own cgroup %s: %w", dir, err)
	}
	return dir, nil
}

// cgroup is one child's cgroup.
type cgroup struct {
	dir string
}

var cgroupSeq atomic.Uint64

// newCgroup creates a child cgroup under the tool's own, or returns nil
// when that is not possible here. Failure is normal: a plain terminal
// session has a root-owned cgroup, and containers often have none.
func newCgroup() *cgroup {
	own, err := ownCgroupDir()
	if err != nil {
		return nil
	}
	dir := filepath.Join(own, fmt.Sprintf("vdispatch-%d-%d", os.Getpid(), cgroupSeq.Add(1)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil
	}
	return &cgroup{dir: dir}
}

// openFD returns a descriptor for clone to place the child into.
func (c *cgroup) openFD() (int, error) {
	return unix.Open(c.dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
}

// cloneIntoCgroup has clone place the child in the cgroup open on fd.
func cloneIntoCgroup(attr *syscall.SysProcAttr, fd int) {
	attr.UseCgroupFD = true
	attr.CgroupFD = fd
}

// members lists the pids in the cgroup.
func (c *cgroup) members() ([]int, error) {
	b, err := os.ReadFile(filepath.Join(c.dir, "cgroup.procs"))
	if err != nil {
		return nil, fmt.Errorf("procs: read cgroup members: %w", err)
	}
	var out []int
	for _, f := range bytes.Fields(b) {
		if pid, err := strconv.Atoi(string(f)); err == nil {
			out = append(out, pid)
		}
	}
	return out, nil
}

func (c *cgroup) write(name, value string) error {
	return os.WriteFile(filepath.Join(c.dir, name), []byte(value), 0o644)
}

// freeze stops every member, including one in the middle of a fork, so
// nothing can change its pid while the members are read and killed.
func (c *cgroup) freeze() error { return c.write("cgroup.freeze", "1") }

// kill ends every member. cgroup.kill exists from kernel 5.14 and also
// ends a process forked while the kill runs. Before that the members are
// signalled one by one once the freeze has taken hold. The cgroup stays
// frozen: a fatal signal still ends a frozen process, and a member the
// kill missed stays stopped until a later pass finds it.
func (c *cgroup) kill() error {
	if err := c.write("cgroup.kill", "1"); err == nil {
		return nil
	}
	deadline := time.Now().Add(freezeWait)
	for !c.event("frozen") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	members, err := c.members()
	if err != nil {
		return err
	}
	for _, pid := range members {
		_ = unix.Kill(pid, unix.SIGKILL)
	}
	return nil
}

// event reports whether cgroup.events has the named key set to 1.
func (c *cgroup) event(key string) bool {
	b, err := os.ReadFile(filepath.Join(c.dir, "cgroup.events"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == key+" 1" {
			return true
		}
	}
	return false
}

// populated reports whether any process is still in the cgroup.
func (c *cgroup) populated() bool { return c.event("populated") }

// remove deletes the cgroup once it is empty, waiting up to d.
func (c *cgroup) remove(d time.Duration) error {
	deadline := time.Now().Add(d)
	for c.populated() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if err := os.Remove(c.dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("procs: remove cgroup %s: %w", c.dir, err)
	}
	return nil
}

// preKill freezes then kills the child's cgroup, when it has one, before
// the process-table passes of a teardown. Errors are ignored: the passes
// still run and find whatever is left.
func (p *Process) preKill() {
	if p.cg == nil {
		return
	}
	_ = p.cg.freeze()
	_ = p.cg.kill()
}

// postKill removes the child's cgroup, when it has one, at the end of a
// teardown, waiting up to d for it to empty.
func (p *Process) postKill(d time.Duration) error {
	if p.cg == nil {
		return nil
	}
	return p.cg.remove(d)
}
