//go:build linux

package procs

import (
	"os"
	"os/exec"
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
	// hopper: fork a copy in a new session and exit, until a stop file
	// exists. Each hop records its pid.
	helpers["hopper"] = func() {
		stop := os.Getenv("PROCS_STOP")
		pidfile := os.Getenv("PROCS_PIDFILE")
		for {
			_ = os.WriteFile(pidfile, []byte(strconv.Itoa(os.Getpid())), 0o600)
			if _, err := os.Stat(stop); err == nil {
				return
			}
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = os.Environ()
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				return
			}
			os.Exit(0)
		}
	}
}
