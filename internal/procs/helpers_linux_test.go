//go:build linux

package procs

import (
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

func init() {
	// threadexit: a worker thread keeps counting while the main thread
	// exits, so the process table shows state Z with threads still running.
	helpers["threadexit"] = func() {
		counter := os.Getenv("PROCS_COUNTER")
		go func() {
			for i := 1; ; i++ {
				_ = os.WriteFile(counter, []byte(strconv.Itoa(i)), 0o600)
				time.Sleep(20 * time.Millisecond)
			}
		}()
		time.Sleep(100 * time.Millisecond)
		runtime.LockOSThread()
		_, _, _ = syscall.RawSyscall(syscall.SYS_EXIT, 0, 0, 0)
	}
}
