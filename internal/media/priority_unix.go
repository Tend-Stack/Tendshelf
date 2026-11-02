//go:build linux || darwin || freebsd

package media

import "syscall"

// backgroundNice is the niceness of a long background decode (waveform peaks).
const backgroundNice = 10

// lowerPriority renices a started child so the foreground probes win the CPU. The error is ignored:
// a child that already exited, or a system that refuses, simply keeps its priority.
func lowerPriority(pid int) {
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, pid, backgroundNice)
}
