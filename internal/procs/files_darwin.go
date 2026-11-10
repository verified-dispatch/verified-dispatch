//go:build darwin

package procs

func closeOnExecAll() error { return markFrom("/dev/fd") }
